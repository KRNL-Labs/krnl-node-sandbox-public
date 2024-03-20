package client

import (
	"log"
	"os"

	"github.com/ethereum/go-ethereum/ethclient"
)

/*
Under the hood we are connecting to a local hardhat node
and extending it with krnl specific rpc calls
*/
func GetClient() *ethclient.Client {
	client, err := ethclient.Dial(os.Getenv("ETH_JSON_RPC")) // hardhat local node
	if err != nil {
		log.Fatal(err)
	}

	return client
}

/*
Connecting to the anvil chain that has the avs and el contracts deployed
*/
func GetElClient() *ethclient.Client {
	anvilChain := os.Getenv("ANVIL_CHAIN")
	if anvilChain == "" {
		anvilChain = "http://127.0.0.1:8546" // local run
	}
	client, err := ethclient.Dial(anvilChain) // anvil local node
	if err != nil {
		log.Fatal(err)
	}

	return client
}

/*
Use this Client to make a transaction to AA Bundler
*/
func GetWsClient() *ethclient.Client {
	client, err := ethclient.Dial(os.Getenv("SEPOLIA_WS_ENDPOINT"))
	if err != nil {
		log.Fatal(err)
	}

	return client
}
