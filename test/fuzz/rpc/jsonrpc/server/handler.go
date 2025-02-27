package handler

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/klyed/tendermint/libs/log"
	rs "github.com/klyed/tendermint/rpc/jsonrpc/server"
	types "github.com/klyed/tendermint/rpc/jsonrpc/types"
)

var rpcFuncMap = map[string]*rs.RPCFunc{
	"c": rs.NewRPCFunc(func(s string, i int) (string, int) { return "foo", 200 }, "s,i"),
}
var mux *http.ServeMux

func init() {
	mux = http.NewServeMux()
	lgr := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	rs.RegisterRPCFuncs(mux, rpcFuncMap, lgr)
}

func Fuzz(data []byte) int {
	if len(data) == 0 {
		return -1
	}

	req, _ := http.NewRequest("POST", "http://localhost/", bytes.NewReader(data))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	res := rec.Result()
	blob, err := ioutil.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}
	if err := res.Body.Close(); err != nil {
		panic(err)
	}
	if len(blob) > 0 {
		recv := new(types.RPCResponse)
		if err := json.Unmarshal(blob, recv); err != nil {
			panic(err)
		}
	}
	return 1
}
