package main

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"
	bolt "go.etcd.io/bbolt"
)

// StateStore provides persistent EVM state storage using BoltDB.
// Bucket structure matches iotex-core's namespaces:
//
//	"Account"  → key: addr_bytes → value: serialized account state
//	"Code"     → key: codeHash   → value: contract bytecode
//	"Contract" → key: addr+slot  → value: storage value
//	"_meta"    → key: "height"   → value: uint64 big-endian
type StateStore struct {
	db     *bolt.DB
	height atomic.Uint64
	logger *zap.Logger
}

// Well-known namespace strings matching iotex-core's constants.
const (
	nsAccount  = "Account"
	nsCode     = "Code"
	nsContract = "Contract"
	nsMeta     = "_meta"
	metaHeight = "height"
)

// OpenStateStore opens or creates a BoltDB state store at the given path.
func OpenStateStore(path string, logger *zap.Logger) (*StateStore, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{
		NoSync:         false,
		NoFreelistSync: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open bolt db: %w", err)
	}

	s := &StateStore{db: db, logger: logger}

	// Ensure buckets exist and load height
	err = db.Update(func(tx *bolt.Tx) error {
		for _, ns := range []string{nsAccount, nsCode, nsContract, nsMeta} {
			if _, err := tx.CreateBucketIfNotExists([]byte(ns)); err != nil {
				return err
			}
		}
		// Load persisted height
		if b := tx.Bucket([]byte(nsMeta)); b != nil {
			if v := b.Get([]byte(metaHeight)); len(v) == 8 {
				s.height.Store(binary.BigEndian.Uint64(v))
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("init buckets: %w", err)
	}

	logger.Info("state store opened",
		zap.String("path", path),
		zap.Uint64("height", s.height.Load()))
	return s, nil
}

// Close closes the BoltDB.
func (s *StateStore) Close() error {
	return s.db.Close()
}

// Height returns the current synced height.
func (s *StateStore) Height() uint64 {
	return s.height.Load()
}

// ApplyDiff applies a single block's state diff entries atomically.
func (s *StateStore) ApplyDiff(height uint64, entries []*stateDiffEntry) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, e := range entries {
			bucket := tx.Bucket([]byte(e.Namespace))
			if bucket == nil {
				// Create bucket on-the-fly for unknown namespaces
				var err error
				bucket, err = tx.CreateBucketIfNotExists([]byte(e.Namespace))
				if err != nil {
					return fmt.Errorf("create bucket %s: %w", e.Namespace, err)
				}
			}
			switch e.WriteType {
			case 0: // Put
				if err := bucket.Put(e.Key, e.Value); err != nil {
					return err
				}
			case 1: // Delete
				if err := bucket.Delete(e.Key); err != nil {
					return err
				}
			}
		}
		// Update height
		hBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(hBuf, height)
		if err := tx.Bucket([]byte(nsMeta)).Put([]byte(metaHeight), hBuf); err != nil {
			return err
		}
		return nil
	})
}

// SetHeight updates the stored height after applying diffs.
func (s *StateStore) SetHeight(h uint64) {
	s.height.Store(h)
}

// Get reads a value by namespace and key. Returns nil if not found.
func (s *StateStore) Get(namespace string, key []byte) ([]byte, error) {
	var val []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(namespace))
		if b == nil {
			return nil
		}
		v := b.Get(key)
		if v != nil {
			val = make([]byte, len(v))
			copy(val, v)
		}
		return nil
	})
	return val, err
}

// Stats returns basic stats about the store.
func (s *StateStore) Stats() map[string]int {
	stats := make(map[string]int)
	s.db.View(func(tx *bolt.Tx) error {
		for _, ns := range []string{nsAccount, nsCode, nsContract} {
			b := tx.Bucket([]byte(ns))
			if b != nil {
				stats[ns] = b.Stats().KeyN
			}
		}
		return nil
	})
	return stats
}
