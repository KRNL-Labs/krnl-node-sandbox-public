package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/gabkov/krnl-node/client"
	"github.com/gabkov/krnl-node/faas"
)

type RegisterDapp struct {
	DappName string `json:"dappName" binding:"required"`
}

type RegisteredDapp struct {
	AccessToken             string `json:"accessToken" binding:"required"`
	TokenAuthorityPublicKey string `json:"tokenAuthorityPublicKey" binding:"required"`
}

type TxRequest struct {
	AccessToken string `json:"accessToken" binding:"required"`
	Message     string `json:"message" binding:"required"`
}

type SignatureToken struct {
	SignatureToken string `json:"signatureToken" binding:"required"`
	Hash           string `json:"hash" binding:"required"`
}

type Krnl struct{}

// TODO: explain comment
func (t *Krnl) TransactionRequest(txRequest *TxRequest) (SignatureToken, error) {
	log.Println("krnl_transactionRequest")
	txRequestPayload, err := json.Marshal(txRequest)
	if err != nil {
		log.Println("Error marshalling JSON:", err)
	}

	body, err := callTokenAuthority("/tx-request", txRequestPayload)
	if err != nil {
		log.Println(err)
		return SignatureToken{}, errors.New(err.Error()) // reject or error
	}

	var signatureToken SignatureToken
	err = json.Unmarshal(body, &signatureToken)
	if err != nil {
		log.Println("error unmarshalling response JSON:", err)
	}

	return signatureToken, nil
}

// TODO: explain 
func (t *Krnl) SendRawTransaction(rawTx string) (string, error) {
	log.Println("krnl_sendRawTransaction")

	client := client.GetClient()

	rawTxBytes, err := hex.DecodeString(rawTx[2:])

	tx := new(types.Transaction)
	err = tx.UnmarshalBinary(rawTxBytes)
	if err != nil {
		log.Fatal("err:", err)
	}

	// Simulate stopping tx here
	// grabbing the requested FaaS services from the end of the input-data
	separator := "000000000000000000000000000000000000000000000000000000000000003a" // :
	res := strings.Split(hexutil.Encode(tx.Data()), separator)

	// if len is more than 1 some message is concatenated to the end of the input-data
	if len(res) > 1 {
		for i := 1; i < len(res); i++ {
			faasRequest, err := hex.DecodeString(res[i])
			if err != nil {
				log.Fatal(err)
			}
			// TODO: mock faas call comment explanation
			err = faas.CallService(string(bytes.Trim(faasRequest, "\x00")), tx)
			if err != nil {
				log.Println(err)
				return "", err
			}
		}
	}

	err = client.SendTransaction(context.Background(), tx)
	if err != nil {
		log.Println(err)
		return "", err
	}

	log.Printf("tx sent: %s", tx.Hash().Hex())

	return tx.Hash().Hex(), nil
}


// TODO: explanation comment
func callTokenAuthority(path string, payload []byte) ([]byte, error) {
	tokenAuthority := os.Getenv("TOKEN_AUTHORITY")
	if tokenAuthority == "" {
		tokenAuthority = "http://127.0.0.1:8181" // local run
	}
	req, err := http.NewRequest("POST", tokenAuthority+path, bytes.NewBuffer(payload))
	if err != nil {
		log.Println("Error creating request:", err)
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Println("Error sending request:", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, errors.New("Transaction rejected: invalid access token")
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("Error reading response body:", err)
		return nil, err
	}

	return body, nil
}