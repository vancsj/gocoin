package wallet

import (
	"os"
	"fmt"
	"bytes"
	"io/ioutil"
	"github.com/piotrnar/gocoin/lib/btc"
	"github.com/piotrnar/gocoin/lib/qdb"
	"github.com/piotrnar/gocoin/lib/chain"
	"github.com/piotrnar/gocoin/lib/others/sys"
	"github.com/piotrnar/gocoin/client/common"
)

type pendingSI struct {
	db *qdb.DB
	k qdb.KeyType
	rec *chain.OneWalkRecord
}

type stealthCacheRec struct {
	h160 [20]byte
	addr *btc.BtcAddr
	d [32]byte
}

var (
	ArmedStealthSecrets [][]byte
	StealthSecrets [][]byte
	newStealthIndexes []pendingSI

	StealthAdCache []stealthCacheRec
)

func FreeStealthSecrets() {
	for i:=range StealthSecrets {
		sys.ClearBuffer(StealthSecrets[i])
	}
	StealthSecrets = nil
}

func FetchStealthKeys() {
	FreeStealthSecrets()
	dir := common.GocoinHomeDir+"wallet"+string(os.PathSeparator)+"stealth"+string(os.PathSeparator)
	fis, er := ioutil.ReadDir(dir)
	if er == nil {
		for i := range fis {
			if !fis[i].IsDir() && fis[i].Size()>=32 {
				d := sys.GetRawData(dir+fis[i].Name())
				if len(d)!=32 {
					fmt.Println("Error reading key from", dir+fis[i].Name(), len(d))
				} else {
					StealthSecrets = append(StealthSecrets, d)
				}
			}
		}
	} else {
		//println("ioutil.ReadDir", er.Error())
		os.MkdirAll(dir, 0700)
	}

	if !PrecachingComplete {
		if len(StealthSecrets)==0 {
			fmt.Println("Place secrets of your stealth keys in", dir, " (use 'arm' to load more)")
		} else {
			fmt.Println(len(StealthSecrets), "stealth keys found in", dir, " (use 'arm' to load more)")
		}
	}
	return
}


func FindStealthSecret(sa *btc.StealthAddr) (d []byte) {
	for i := range StealthSecrets {
		if bytes.Equal(btc.PublicFromPrivate(StealthSecrets[i], true), sa.ScanKey[:]) {
			return StealthSecrets[i]
		}
	}
	for i := range ArmedStealthSecrets {
		if bytes.Equal(btc.PublicFromPrivate(ArmedStealthSecrets[i], true), sa.ScanKey[:]) {
			return ArmedStealthSecrets[i]
		}
	}
	return
}


// It is assumed that you call this function only after rec.IsStealthIdx() was true
func CheckStealthRec(db *qdb.DB, k qdb.KeyType, rec *chain.OneWalkRecord,
	addr *btc.BtcAddr, d []byte, inbrowse bool) (fl uint32, uo *chain.OneUnspentTx) {
	sth_scr := rec.Script()
	sa := addr.StealthAddr
	if sa.CheckNonce(sth_scr[3:]) {
		vo := rec.VOut() // get the spending output
		var spend_v []byte
		if inbrowse {
			spend_v = db.GetNoMutex(qdb.KeyType(uint64(k) ^ uint64(vo) ^ uint64(vo+1)))
		} else {
			spend_v = db.Get(qdb.KeyType(uint64(k) ^ uint64(vo) ^ uint64(vo+1)))
		}
		if spend_v!=nil {
			rec = chain.NewWalkRecord(spend_v)

			if rec.IsP2KH() {
				var h160 [20]byte
				c := btc.StealthDH(sth_scr[7:40], d)
				spen_exp := btc.DeriveNextPublic(sa.SpendKeys[0][:], c)
				btc.RimpHash(spen_exp, h160[:])
				if bytes.Equal(rec.Script()[3:23], h160[:]) {
					adr := btc.NewAddrFromHash160(h160[:], btc.AddrVerPubkey(common.CFG.Testnet))
					uo = rec.ToUnspent(adr)
					adr.StealthAddr = sa
					adr.Extra = addr.Extra
					uo.StealthC = c
				}
			} else {
				fl = chain.WALK_NOMORE
			}
		} else {
			fl = chain.WALK_NOMORE
		}
	}
	return
}


func StealthNotify(db *qdb.DB, k qdb.KeyType, rec *chain.OneWalkRecord) uint32 {
	BalanceMutex.Lock()
	newStealthIndexes = append(newStealthIndexes, pendingSI{db:db, k:k, rec:rec})
	BalanceMutex.Unlock()
	return 0
}


// Go through all the stealth indexes found in the last block
func DoPendingStealths() {
	if len(newStealthIndexes) > 0 {
		var update_wallet bool
		BalanceMutex.Lock()
		for i := range newStealthIndexes {
			for ai := range StealthAdCache {
				db := newStealthIndexes[i].db
				key := newStealthIndexes[i].k
				fl, uo := CheckStealthRec(db, key, newStealthIndexes[i].rec, StealthAdCache[ai].addr,
					StealthAdCache[ai].d[:], false)
				if fl!=0 {
					db.ApplyFlags(key, qdb.NO_CACHE|qdb.NO_BROWSE)
					break
				}
				if uo != nil {
					if rec, ok := CachedAddrs[StealthAdCache[ai].h160]; ok {
						rec.Value += uo.Value
						uo.BtcAddr = StealthAdCache[ai].addr
						CacheUnspent[rec.CacheIndex].AllUnspentTx = append(CacheUnspent[rec.CacheIndex].AllUnspentTx, uo)
						CacheUnspentIdx[uo.TxPrevOut.UIdx()] = &OneCachedUnspentIdx{Index: rec.CacheIndex, Record: uo}
						if rec.InWallet {
							update_wallet = true
						}
					} else {
						println("Such address is not cached??? This should not happen.")
					}
					break
				}
			}
		}
		newStealthIndexes = nil
		if update_wallet {
			sync_wallet()
		}
		BalanceMutex.Unlock()
	}
}
