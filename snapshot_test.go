package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// writeTestSnapshot creates a valid IOSWSNAP file at the given path.
func writeTestSnapshot(t *testing.T, path string, height uint64, entries []testSnapEntry) {
	t.Helper()

	var body bytes.Buffer

	// Header
	body.WriteString(snapshotMagic)
	binary.Write(&body, binary.BigEndian, uint32(snapshotVersion))
	binary.Write(&body, binary.BigEndian, height)

	// Entries
	for _, e := range entries {
		body.WriteByte(entryMarker)
		body.WriteByte(uint8(len(e.ns)))
		body.WriteString(e.ns)
		binary.Write(&body, binary.BigEndian, uint32(len(e.key)))
		body.Write(e.key)
		binary.Write(&body, binary.BigEndian, uint32(len(e.val)))
		body.Write(e.val)
	}

	// End marker
	body.WriteByte(endMarker)

	// Compute digest over header + entries + end marker
	digest := sha256.Sum256(body.Bytes())

	// Trailer (not part of digest)
	binary.Write(&body, binary.BigEndian, uint64(len(entries)))
	body.Write(digest[:])
	body.WriteString(snapshotEnd)

	// Gzip compress
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	if _, err := gz.Write(body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

type testSnapEntry struct {
	ns  string
	key []byte
	val []byte
}

func TestLoadSnapshot(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()

	t.Run("basic", func(t *testing.T) {
		snapPath := filepath.Join(dir, "test.snap")
		dbPath := filepath.Join(dir, "basic.db")

		// Create a snapshot with 3 Account entries
		entries := []testSnapEntry{
			{ns: nsAccount, key: bytes.Repeat([]byte{0x01}, 20), val: encodeTestAccount(1, "100", nil, nil)},
			{ns: nsAccount, key: bytes.Repeat([]byte{0x02}, 20), val: encodeTestAccount(5, "999", nil, nil)},
			{ns: nsCode, key: bytes.Repeat([]byte{0xAA}, 32), val: []byte{0x60, 0x00}},
		}
		writeTestSnapshot(t, snapPath, 1000, entries)

		store, err := OpenStateStore(dbPath, logger)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		h, n, err := LoadSnapshot(snapPath, store, logger)
		if err != nil {
			t.Fatal(err)
		}
		if h != 1000 {
			t.Errorf("height = %d, want 1000", h)
		}
		if n != 3 {
			t.Errorf("entries = %d, want 3", n)
		}
		if store.Height() != 1000 {
			t.Errorf("store height = %d, want 1000", store.Height())
		}

		// Verify account can be read
		acct, err := store.GetAccount(bytes.Repeat([]byte{0x01}, 20))
		if err != nil {
			t.Fatal(err)
		}
		if acct == nil {
			t.Fatal("account not found")
		}
		if acct.Nonce != 1 {
			t.Errorf("nonce = %d, want 1", acct.Nonce)
		}

		// Verify code can be read
		code, err := store.GetCode(bytes.Repeat([]byte{0xAA}, 32))
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 2 || code[0] != 0x60 {
			t.Errorf("code = %x, want 6000", code)
		}
	})

	t.Run("skip_if_store_ahead", func(t *testing.T) {
		snapPath := filepath.Join(dir, "old.snap")
		dbPath := filepath.Join(dir, "ahead.db")

		writeTestSnapshot(t, snapPath, 500, []testSnapEntry{
			{ns: nsAccount, key: bytes.Repeat([]byte{0x01}, 20), val: encodeTestAccount(1, "100", nil, nil)},
		})

		store, err := OpenStateStore(dbPath, logger)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		// Advance store to height 1000
		store.ApplyDiff(1000, nil)

		h, n, err := LoadSnapshot(snapPath, store, logger)
		if err != nil {
			t.Fatal(err)
		}
		if h != 500 {
			t.Errorf("height = %d, want 500", h)
		}
		if n != 0 {
			t.Errorf("entries = %d, want 0 (skipped)", n)
		}
	})

	t.Run("corrupt_digest", func(t *testing.T) {
		snapPath := filepath.Join(dir, "corrupt.snap")
		dbPath := filepath.Join(dir, "corrupt.db")

		// Write valid snapshot, then corrupt it
		entries := []testSnapEntry{
			{ns: nsAccount, key: bytes.Repeat([]byte{0x01}, 20), val: encodeTestAccount(1, "100", nil, nil)},
		}

		// Manual corruption: build the file but flip a digest byte
		var body bytes.Buffer
		body.WriteString(snapshotMagic)
		binary.Write(&body, binary.BigEndian, uint32(snapshotVersion))
		binary.Write(&body, binary.BigEndian, uint64(2000))

		for _, e := range entries {
			body.WriteByte(entryMarker)
			body.WriteByte(uint8(len(e.ns)))
			body.WriteString(e.ns)
			binary.Write(&body, binary.BigEndian, uint32(len(e.key)))
			body.Write(e.key)
			binary.Write(&body, binary.BigEndian, uint32(len(e.val)))
			body.Write(e.val)
		}
		body.WriteByte(endMarker)

		digest := sha256.Sum256(body.Bytes())
		digest[0] ^= 0xFF // corrupt

		binary.Write(&body, binary.BigEndian, uint64(len(entries)))
		body.Write(digest[:])
		body.WriteString(snapshotEnd)

		f, _ := os.Create(snapPath)
		gz := gzip.NewWriter(f)
		gz.Write(body.Bytes())
		gz.Close()
		f.Close()

		store, _ := OpenStateStore(dbPath, logger)
		defer store.Close()

		_, _, err := LoadSnapshot(snapPath, store, logger)
		if err == nil {
			t.Error("expected error for corrupt digest")
		}
	})
}

func TestStateStoreGetAccount(t *testing.T) {
	logger := zap.NewNop()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := OpenStateStore(dbPath, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Insert an account
	addrHash := bytes.Repeat([]byte{0x42}, 20)
	acctData := encodeTestAccount(10, "5000000000000000000", nil, nil)

	err = store.ApplyDiff(100, []*stateDiffEntry{
		{WriteType: WriteTypePut, Namespace: nsAccount, Key: addrHash, Value: acctData},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read it back
	acct, err := store.GetAccount(addrHash)
	if err != nil {
		t.Fatal(err)
	}
	if acct == nil {
		t.Fatal("account not found")
	}
	if acct.Nonce != 10 {
		t.Errorf("nonce = %d, want 10", acct.Nonce)
	}

	// Non-existent account
	acct2, err := store.GetAccount(bytes.Repeat([]byte{0xFF}, 20))
	if err != nil {
		t.Fatal(err)
	}
	if acct2 != nil {
		t.Error("expected nil for non-existent account")
	}
}
