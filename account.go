package main

import (
	"errors"
	"fmt"
	"math/big"

	"google.golang.org/protobuf/encoding/protowire"
)

// IoTXAccount represents a decoded IoTeX account state from BoltDB.
// Fields map to iotex-core's accountpb.Account protobuf:
//
//	field 1: nonce   (varint)
//	field 2: balance (string, big.Int decimal)
//	field 3: root    (bytes, 32B storage trie root)
//	field 4: codeHash (bytes, contract bytecode hash)
//
// Fields 5-7 (isCandidate, votingWeight, type) are ignored.
type IoTXAccount struct {
	Nonce    uint64
	Balance  *big.Int
	Root     []byte // 32B storage trie root
	CodeHash []byte // contract code hash
}

// DecodeAccount decodes an IoTeX Account protobuf from raw bytes using protowire.
// Returns an error if the data is malformed. Missing fields default to zero values.
func DecodeAccount(data []byte) (*IoTXAccount, error) {
	if len(data) == 0 {
		return nil, errors.New("empty account data")
	}

	acct := &IoTXAccount{Balance: new(big.Int)}
	buf := data

	for len(buf) > 0 {
		num, wtype, n := protowire.ConsumeTag(buf)
		if n < 0 {
			return nil, fmt.Errorf("invalid protobuf tag at offset %d", len(data)-len(buf))
		}
		buf = buf[n:]

		switch wtype {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(buf)
			if n < 0 {
				return nil, fmt.Errorf("invalid varint for field %d", num)
			}
			buf = buf[n:]
			if num == 1 {
				acct.Nonce = v
			}
			// field 5 (isCandidate) and field 7 (type) are also varint, ignored

		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(buf)
			if n < 0 {
				return nil, fmt.Errorf("invalid bytes for field %d", num)
			}
			buf = buf[n:]
			switch num {
			case 2: // balance: decimal string
				if len(v) > 0 {
					bal, ok := new(big.Int).SetString(string(v), 10)
					if !ok {
						return nil, fmt.Errorf("invalid balance string: %q", string(v))
					}
					acct.Balance = bal
				}
			case 3: // root
				acct.Root = make([]byte, len(v))
				copy(acct.Root, v)
			case 4: // codeHash
				acct.CodeHash = make([]byte, len(v))
				copy(acct.CodeHash, v)
			case 6: // votingWeight: big.Int bytes, ignored
			}

		case protowire.Fixed32Type:
			_, n := protowire.ConsumeFixed32(buf)
			if n < 0 {
				return nil, fmt.Errorf("invalid fixed32 for field %d", num)
			}
			buf = buf[n:]

		case protowire.Fixed64Type:
			_, n := protowire.ConsumeFixed64(buf)
			if n < 0 {
				return nil, fmt.Errorf("invalid fixed64 for field %d", num)
			}
			buf = buf[n:]

		default:
			return nil, fmt.Errorf("unsupported wire type %d for field %d", wtype, num)
		}
	}

	return acct, nil
}

// addressToHash160 converts a 0x-prefixed hex address to its 20-byte representation.
// In iotex-core, Account keys are the raw 20-byte address bytes (bech32 decoded for io1,
// or hex decoded for 0x addresses).
func addressToHash160(addr string) ([]byte, error) {
	if len(addr) < 2 {
		return nil, errors.New("address too short")
	}

	// 0x hex address → decode to 20 bytes
	if addr[:2] == "0x" || addr[:2] == "0X" {
		hex := addr[2:]
		if len(hex) != 40 {
			return nil, fmt.Errorf("invalid hex address length: %d", len(hex))
		}
		b := make([]byte, 20)
		for i := 0; i < 20; i++ {
			hi, err := hexVal(hex[i*2])
			if err != nil {
				return nil, err
			}
			lo, err := hexVal(hex[i*2+1])
			if err != nil {
				return nil, err
			}
			b[i] = hi<<4 | lo
		}
		return b, nil
	}

	return nil, fmt.Errorf("unsupported address format: %q (only 0x supported)", addr)
}

func hexVal(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	default:
		return 0, fmt.Errorf("invalid hex char: %c", c)
	}
}
