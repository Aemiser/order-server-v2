package main

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/sys/windows"
)

const pageSize = 4096
const MEM_PRIVATE = 0x20000

var logf *os.File
var logMu sync.Mutex

func logfPrintf(format string, args ...any) {
	logMu.Lock()
	fmt.Fprintf(logf, format+"\n", args...)
	logf.Sync()
	logMu.Unlock()
}

func main() {
	lf, err := os.Create(`F:\project\go\order_server\keyfind\keyfind.log`)
	if err != nil {
		fmt.Println("log err", err)
		return
	}
	logf = lf
	defer lf.Close()

	dbPath := `C:\Users\aemiser\Documents\WeChat Files\wxid_8t195fu63esh22\Msg\ChatMsg.db`
	if len(os.Args) > 1 {
		dbPath = os.Args[1]
	}
	page1 := make([]byte, 8192)
	f, err := os.Open(dbPath)
	if err != nil {
		logfPrintf("open db: %v", err)
		return
	}
	n, _ := f.Read(page1)
	f.Close()
	page1 = page1[:n]
	logfPrintf("DB=%s page1len=%d head=%x", dbPath, n, page1[:16])

	pids := findPIDs("Weixin.exe")
	logfPrintf("Weixin PIDs: %v", pids)

	patterns := [][]byte{
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x2f, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	}

	found := false
	for _, pid := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_QUERY_INFORMATION, false, pid)
		if err != nil {
			logfPrintf("PID %d open err: %v", pid, err)
			continue
		}
		regions := getRegions(h)
		logfPrintf("PID %d regions=%d", pid, len(regions))
		cands := 0
		for ri, r := range regions {
			if r.State != windows.MEM_COMMIT || r.Type != MEM_PRIVATE {
				continue
			}
			if r.RegionSize == 0 || r.RegionSize > 256*1024*1024 {
				continue
			}
			prot := r.Protect
			if prot == windows.PAGE_NOACCESS || prot&windows.PAGE_GUARD != 0 {
				continue
			}
			data, err := readMem(h, r.BaseAddress, int(r.RegionSize))
			if err != nil || len(data) == 0 {
				continue
			}
			for pi, pat := range patterns {
				off := 0
				for {
					idx := indexOf(data, pat, off)
					if idx < 0 {
						break
					}
					off = idx + 1
					if idx-8 < 0 {
						continue
					}
					ptr := binary.LittleEndian.Uint64(data[idx-8 : idx])
					if ptr < 0x10000 || ptr > 0x7FFFFFFFFFFF {
						continue
					}
					kb, _ := readMem(h, uintptr(ptr), 32)
					if len(kb) != 32 {
						continue
					}
					if !isPotentialKey(kb) {
						continue
					}
					cands++
					if cands%50 == 0 {
						logfPrintf("  pid=%d region=%d checked=%d", pid, ri, cands)
					}
					if rawValidate(kb, page1) {
						logfPrintf(">>> RAW KEY pid=%d pat=%d ptr=0x%x key=%s", pid, pi, ptr, hex.EncodeToString(kb))
						found = true
						break
					}
					if derivedValidate(kb, page1) {
						logfPrintf(">>> DERIVED KEY pid=%d pat=%d ptr=0x%x key=%s", pid, pi, ptr, hex.EncodeToString(kb))
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if found {
				break
			}
		}
		logfPrintf("PID %d done, candidates=%d", pid, cands)
		if found {
			break
		}
	}
	if !found {
		logfPrintf("RESULT: NO KEY FOUND via descriptor scan on 4.2.0.13")
	} else {
		logfPrintf("RESULT: KEY FOUND")
	}
}

func isPotentialKey(k []byte) bool {
	if len(k) != 32 {
		return false
	}
	seen := make(map[byte]struct{}, 32)
	printable := 0
	for _, b := range k {
		seen[b] = struct{}{}
		if 32 <= b && b <= 126 {
			printable++
		}
	}
	if len(seen) < 15 {
		return false
	}
	if printable > 24 {
		return false
	}
	return true
}

func rawValidate(key, p1 []byte) bool {
	for _, P := range []int{4096, 8192} {
		if len(p1) < P {
			continue
		}
		page := p1[:P]
		salt := page[:16]
		macSalt := make([]byte, 16)
		for i := range salt {
			macSalt[i] = salt[i] ^ 0x3a
		}
		K := pbkdf2.Key(key, salt, 256000, 32, sha512.New)
		macKey := pbkdf2.Key(K, macSalt, 2, 32, sha512.New)
		hmacData := page[16 : P-80+16]
		stored := page[P-64 : P]
		mac := hmac.New(sha512.New, macKey)
		mac.Write(hmacData)
		var pn [4]byte
		binary.LittleEndian.PutUint32(pn[:], 1)
		mac.Write(pn[:])
		if hmac.Equal(mac.Sum(nil), stored) {
			return true
		}
	}
	return false
}

func derivedValidate(key, p1 []byte) bool {
	for _, P := range []int{4096, 8192} {
		if len(p1) < P {
			continue
		}
		page := p1[:P]
		salt := page[:16]
		macSalt := make([]byte, 16)
		for i := range salt {
			macSalt[i] = salt[i] ^ 0x3a
		}
		macKey := pbkdf2.Key(key, macSalt, 2, 32, sha512.New)
		hmacData := page[16 : P-80+16]
		stored := page[P-64 : P]
		mac := hmac.New(sha512.New, macKey)
		mac.Write(hmacData)
		var pn [4]byte
		binary.LittleEndian.PutUint32(pn[:], 1)
		mac.Write(pn[:])
		if hmac.Equal(mac.Sum(nil), stored) {
			return true
		}
	}
	return false
}

func findPIDs(name string) []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pids []uint32
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return pids
	}
	for {
		if windows.UTF16ToString(entry.ExeFile[:]) == name {
			pids = append(pids, entry.ProcessID)
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return pids
}

func getRegions(h windows.Handle) []windows.MemoryBasicInformation {
	var regs []windows.MemoryBasicInformation
	var addr uintptr = 0x10000
	const maxAddr = uintptr(0x7FFFFFFFFFFF)
	for addr < maxAddr {
		var mbi windows.MemoryBasicInformation
		if err := windows.VirtualQueryEx(h, addr, &mbi, unsafe.Sizeof(mbi)); err != nil {
			break
		}
		if mbi.State == windows.MEM_COMMIT {
			regs = append(regs, mbi)
		}
		addr = mbi.BaseAddress + mbi.RegionSize
	}
	return regs
}

func readMem(h windows.Handle, addr uintptr, size int) ([]byte, error) {
	buf := make([]byte, size)
	var read uintptr
	err := windows.ReadProcessMemory(h, addr, &buf[0], uintptr(size), &read)
	if err != nil {
		return nil, err
	}
	return buf[:read], nil
}

func indexOf(hay, needle []byte, from int) int {
	if from < 0 || from > len(hay) {
		return -1
	}
	n := len(needle)
	if n == 0 {
		return -1
	}
	for i := from; i+n <= len(hay); i++ {
		if hay[i] == needle[0] && equal(hay[i:i+n], needle) {
			return i
		}
	}
	return -1
}

func equal(a, b []byte) bool {
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
