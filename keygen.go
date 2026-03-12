package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

func runKeygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	outKey := fs.String("out", "", "output file for private key (hex)")
	outAddr := fs.String("addr-out", "", "output file for wallet address (0x...)")
	fs.Parse(args)

	key, err := crypto.GenerateKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating key: %v\n", err)
		os.Exit(1)
	}

	privHex := hex.EncodeToString(crypto.FromECDSA(key))
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()

	if *outKey != "" {
		if err := os.WriteFile(*outKey, []byte(privHex+"\n"), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "error writing key: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println(privHex)
	}

	if *outAddr != "" {
		if err := os.WriteFile(*outAddr, []byte(addr+"\n"), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing address: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Fprintf(os.Stderr, "Address: %s\n", addr)
}

func runAddr(args []string) {
	fs := flag.NewFlagSet("addr", flag.ExitOnError)
	keyFile := fs.String("key", "", "private key file (hex)")
	fs.Parse(args)

	if *keyFile == "" {
		fmt.Fprintf(os.Stderr, "error: --key is required\n")
		os.Exit(1)
	}

	data, err := os.ReadFile(*keyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading key file: %v\n", err)
		os.Exit(1)
	}

	hexKey := strings.TrimSpace(string(data))
	hexKey = strings.TrimPrefix(hexKey, "0x")

	key, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing private key: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(crypto.PubkeyToAddress(key.PublicKey).Hex())
}
