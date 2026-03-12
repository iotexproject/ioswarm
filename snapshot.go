package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"go.uber.org/zap"
)

// Snapshot binary format constants (must match coordinator's SnapshotWriter).
const (
	snapshotMagic   = "IOSWSNAP"          // 8 bytes
	snapshotEnd     = "SNAPEND\x00"       // 8 bytes
	snapshotVersion = 1                   // current format version
	entryMarker     = byte(0x01)          // entry record prefix
	endMarker       = byte(0x00)          // end-of-entries marker
)

// LoadSnapshot reads a gzip-compressed IOSWSNAP file and loads all entries
// into the StateStore. Returns the snapshot height and number of entries loaded.
//
// Format:
//
//	[Header] magic(8) + version(uint32) + height(uint64)
//	[Entry]  0x01 + ns_len(uint8) + namespace + key_len(uint32) + key + val_len(uint32) + value
//	[End]    0x00
//	[Trailer] entry_count(uint64) + sha256(32) + "SNAPEND\0"(8)
//
// The entire file is gzip-compressed.
func LoadSnapshot(path string, store *StateStore, logger *zap.Logger) (height uint64, entries int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open snapshot: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, 0, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	// We need to compute SHA256 over the uncompressed payload (header + entries + end marker),
	// and also read the trailer. Use a TeeReader that writes to a hash.
	hasher := sha256.New()
	r := io.TeeReader(gz, hasher)

	// --- Header ---
	magic := make([]byte, 8)
	if _, err := io.ReadFull(r, magic); err != nil {
		return 0, 0, fmt.Errorf("read magic: %w", err)
	}
	if string(magic) != snapshotMagic {
		return 0, 0, fmt.Errorf("invalid magic: %q", magic)
	}

	var version uint32
	if err := binary.Read(r, binary.BigEndian, &version); err != nil {
		return 0, 0, fmt.Errorf("read version: %w", err)
	}
	if version != snapshotVersion {
		return 0, 0, fmt.Errorf("unsupported snapshot version: %d", version)
	}

	if err := binary.Read(r, binary.BigEndian, &height); err != nil {
		return 0, 0, fmt.Errorf("read height: %w", err)
	}

	// If store already has a higher height, skip loading
	if store.Height() >= height {
		logger.Info("snapshot already applied, skipping",
			zap.Uint64("snapshot_height", height),
			zap.Uint64("store_height", store.Height()))
		return height, 0, nil
	}

	// --- Entries ---
	var diffs []*stateDiffEntry
	marker := make([]byte, 1)

	for {
		if _, err := io.ReadFull(r, marker); err != nil {
			return 0, 0, fmt.Errorf("read entry marker: %w", err)
		}

		if marker[0] == endMarker {
			break
		}
		if marker[0] != entryMarker {
			return 0, 0, fmt.Errorf("unexpected marker byte: 0x%02x", marker[0])
		}

		// namespace
		var nsLen uint8
		if err := binary.Read(r, binary.BigEndian, &nsLen); err != nil {
			return 0, 0, fmt.Errorf("read ns_len: %w", err)
		}
		ns := make([]byte, nsLen)
		if _, err := io.ReadFull(r, ns); err != nil {
			return 0, 0, fmt.Errorf("read namespace: %w", err)
		}

		// key
		var keyLen uint32
		if err := binary.Read(r, binary.BigEndian, &keyLen); err != nil {
			return 0, 0, fmt.Errorf("read key_len: %w", err)
		}
		key := make([]byte, keyLen)
		if _, err := io.ReadFull(r, key); err != nil {
			return 0, 0, fmt.Errorf("read key: %w", err)
		}

		// value
		var valLen uint32
		if err := binary.Read(r, binary.BigEndian, &valLen); err != nil {
			return 0, 0, fmt.Errorf("read val_len: %w", err)
		}
		val := make([]byte, valLen)
		if _, err := io.ReadFull(r, val); err != nil {
			return 0, 0, fmt.Errorf("read value: %w", err)
		}

		diffs = append(diffs, &stateDiffEntry{
			WriteType: WriteTypePut,
			Namespace: string(ns),
			Key:       key,
			Value:     val,
		})
		entries++
	}

	// At this point, hasher has the digest of header + entries + end marker.
	// Stop hashing — trailer is NOT part of the digest.
	digestWant := hasher.Sum(nil)

	// --- Trailer (read directly from gz, not through TeeReader) ---
	var trailerEntryCount uint64
	if err := binary.Read(gz, binary.BigEndian, &trailerEntryCount); err != nil {
		return 0, 0, fmt.Errorf("read trailer entry_count: %w", err)
	}
	if int(trailerEntryCount) != entries {
		return 0, 0, fmt.Errorf("entry count mismatch: trailer=%d actual=%d", trailerEntryCount, entries)
	}

	digestGot := make([]byte, 32)
	if _, err := io.ReadFull(gz, digestGot); err != nil {
		return 0, 0, fmt.Errorf("read trailer digest: %w", err)
	}

	endMagic := make([]byte, 8)
	if _, err := io.ReadFull(gz, endMagic); err != nil {
		return 0, 0, fmt.Errorf("read end magic: %w", err)
	}
	if string(endMagic) != snapshotEnd {
		return 0, 0, fmt.Errorf("invalid end magic: %q", endMagic)
	}

	// Verify SHA256 digest
	if !bytesEqual(digestWant, digestGot) {
		return 0, 0, errors.New("snapshot SHA256 digest mismatch")
	}

	// Apply all entries in a single batch
	if err := store.ApplyDiff(height, diffs); err != nil {
		return 0, 0, fmt.Errorf("apply snapshot entries: %w", err)
	}

	logger.Info("snapshot loaded",
		zap.Uint64("height", height),
		zap.Int("entries", entries))

	return height, entries, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
