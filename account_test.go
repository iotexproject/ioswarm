package main

import (
	"math/big"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// encodeTestAccount builds a protobuf-encoded account for testing.
func encodeTestAccount(nonce uint64, balance string, root, codeHash []byte) []byte {
	var buf []byte
	// field 1: nonce (varint)
	buf = protowire.AppendTag(buf, 1, protowire.VarintType)
	buf = protowire.AppendVarint(buf, nonce)
	// field 2: balance (bytes/string)
	if balance != "" {
		buf = protowire.AppendTag(buf, 2, protowire.BytesType)
		buf = protowire.AppendBytes(buf, []byte(balance))
	}
	// field 3: root (bytes)
	if len(root) > 0 {
		buf = protowire.AppendTag(buf, 3, protowire.BytesType)
		buf = protowire.AppendBytes(buf, root)
	}
	// field 4: codeHash (bytes)
	if len(codeHash) > 0 {
		buf = protowire.AppendTag(buf, 4, protowire.BytesType)
		buf = protowire.AppendBytes(buf, codeHash)
	}
	return buf
}

func TestDecodeAccount(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		data := encodeTestAccount(42, "1000000000000000000", nil, nil)
		acct, err := DecodeAccount(data)
		if err != nil {
			t.Fatal(err)
		}
		if acct.Nonce != 42 {
			t.Errorf("nonce = %d, want 42", acct.Nonce)
		}
		want, _ := new(big.Int).SetString("1000000000000000000", 10)
		if acct.Balance.Cmp(want) != 0 {
			t.Errorf("balance = %s, want %s", acct.Balance, want)
		}
	})

	t.Run("with_root_and_codehash", func(t *testing.T) {
		root := make([]byte, 32)
		root[0] = 0xAB
		codeHash := make([]byte, 32)
		codeHash[31] = 0xCD

		data := encodeTestAccount(100, "999", root, codeHash)
		acct, err := DecodeAccount(data)
		if err != nil {
			t.Fatal(err)
		}
		if acct.Nonce != 100 {
			t.Errorf("nonce = %d, want 100", acct.Nonce)
		}
		if acct.Root[0] != 0xAB {
			t.Errorf("root[0] = %x, want AB", acct.Root[0])
		}
		if acct.CodeHash[31] != 0xCD {
			t.Errorf("codeHash[31] = %x, want CD", acct.CodeHash[31])
		}
	})

	t.Run("zero_balance", func(t *testing.T) {
		data := encodeTestAccount(0, "0", nil, nil)
		acct, err := DecodeAccount(data)
		if err != nil {
			t.Fatal(err)
		}
		if acct.Balance.Sign() != 0 {
			t.Errorf("balance = %s, want 0", acct.Balance)
		}
	})

	t.Run("empty_data", func(t *testing.T) {
		_, err := DecodeAccount(nil)
		if err == nil {
			t.Error("expected error for empty data")
		}
	})

	t.Run("unknown_fields_ignored", func(t *testing.T) {
		// Add a field 5 (isCandidate = true) and field 7 (type = 1)
		data := encodeTestAccount(5, "100", nil, nil)
		data = protowire.AppendTag(data, 5, protowire.VarintType)
		data = protowire.AppendVarint(data, 1)
		data = protowire.AppendTag(data, 7, protowire.VarintType)
		data = protowire.AppendVarint(data, 1)

		acct, err := DecodeAccount(data)
		if err != nil {
			t.Fatal(err)
		}
		if acct.Nonce != 5 {
			t.Errorf("nonce = %d, want 5", acct.Nonce)
		}
	})
}

func TestAddressToHash160(t *testing.T) {
	t.Run("valid_0x", func(t *testing.T) {
		addr := "0xd31D0d6d4018B50D7a138cd0c360958dDA44A970"
		h, err := addressToHash160(addr)
		if err != nil {
			t.Fatal(err)
		}
		if len(h) != 20 {
			t.Errorf("len = %d, want 20", len(h))
		}
		// first byte should be 0xd3
		if h[0] != 0xd3 {
			t.Errorf("h[0] = %x, want d3", h[0])
		}
	})

	t.Run("invalid_length", func(t *testing.T) {
		_, err := addressToHash160("0xabc")
		if err == nil {
			t.Error("expected error for short hex")
		}
	})

	t.Run("unsupported_format", func(t *testing.T) {
		_, err := addressToHash160("io1abc")
		if err == nil {
			t.Error("expected error for io1 address")
		}
	})
}
