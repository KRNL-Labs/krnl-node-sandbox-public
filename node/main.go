package main

import (
	"log"
	"net/http"

	hs "github.com/gabkov/krnl-node/httpserver"
	"github.com/gabkov/krnl-node/rpc"
	"github.com/gabkov/krnl-node/service"
)

/*
Author: Gabor Kovacs | gabor.kovacs995@gmail.com | gabkov
*/

func main() {
	// TODO: add comment and describe what it does/how
	srv := rpc.NewServer()

	// TODO: comment
	if err := srv.RegisterName("krnl", new(service.Krnl)); err != nil {
		panic(err)
	}

	// TODO: comment
	if err := srv.RegisterName("eth", new(service.Eth)); err != nil {
		panic(err)
	}

	// TODO:
	if err := srv.RegisterName("net", new(service.Net)); err != nil {
		panic(err)
	}
	log.Println("starting")
	httpsrv := hs.NewHttpServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.ServeHTTP(w, r)
	}))

	defer httpsrv.Close()
	defer srv.Stop()
}
