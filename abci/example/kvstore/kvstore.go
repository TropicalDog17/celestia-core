package kvstore

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"

	"github.com/lazyledger/lazyledger-core/abci/example/code"
	"github.com/lazyledger/lazyledger-core/abci/types"
	dbm "github.com/lazyledger/lazyledger-core/libs/db"
	memdb "github.com/lazyledger/lazyledger-core/libs/db/memdb"
	tmproto "github.com/lazyledger/lazyledger-core/proto/tendermint/types"
	tmtypes "github.com/lazyledger/lazyledger-core/types"
	"github.com/lazyledger/lazyledger-core/version"
	"github.com/lazyledger/nmt"
)

var (
	stateKey        = []byte("stateKey")
	kvPairPrefixKey = []byte("kvPairKey:")

	ProtocolVersion uint64 = 0x1
)

type State struct {
	db      dbm.DB
	Size    int64  `json:"size"`
	Height  int64  `json:"height"`
	AppHash []byte `json:"app_hash"`
}

func loadState(db dbm.DB) State {
	var state State
	state.db = db
	stateBytes, err := db.Get(stateKey)
	if err != nil {
		panic(err)
	}
	if len(stateBytes) == 0 {
		return state
	}
	err = json.Unmarshal(stateBytes, &state)
	if err != nil {
		panic(err)
	}
	return state
}

func saveState(state State) {
	stateBytes, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}
	err = state.db.Set(stateKey, stateBytes)
	if err != nil {
		panic(err)
	}
}

func prefixKey(key []byte) []byte {
	return append(kvPairPrefixKey, key...)
}

//---------------------------------------------------

var _ types.Application = (*Application)(nil)

type Application struct {
	types.BaseApplication

	state        State
	RetainBlocks int64 // blocks to retain after commit (via ResponseCommit.RetainHeight)
}

func NewApplication() *Application {
	state := loadState(memdb.NewDB())
	return &Application{state: state}
}

func (app *Application) Info(req types.RequestInfo) (resInfo types.ResponseInfo) {
	return types.ResponseInfo{
		Data:             fmt.Sprintf("{\"size\":%v}", app.state.Size),
		Version:          version.ABCIVersion,
		AppVersion:       ProtocolVersion,
		LastBlockHeight:  app.state.Height,
		LastBlockAppHash: app.state.AppHash,
	}
}

// tx is either "key=value" or just arbitrary bytes
func (app *Application) DeliverTx(req types.RequestDeliverTx) types.ResponseDeliverTx {
	var key, value []byte
	parts := bytes.Split(req.Tx, []byte("="))
	if len(parts) == 2 {
		key, value = parts[0], parts[1]
	} else {
		key, value = req.Tx, req.Tx
	}

	err := app.state.db.Set(prefixKey(key), value)
	if err != nil {
		panic(err)
	}
	app.state.Size++

	events := []types.Event{
		{
			Type: "app",
			Attributes: []types.EventAttribute{
				{Key: []byte("creator"), Value: []byte("Cosmoshi Netowoko"), Index: true},
				{Key: []byte("key"), Value: key, Index: true},
				{Key: []byte("index_key"), Value: []byte("index is working"), Index: true},
				{Key: []byte("noindex_key"), Value: []byte("index is working"), Index: false},
			},
		},
	}

	return types.ResponseDeliverTx{Code: code.CodeTypeOK, Events: events}
}

func (app *Application) CheckTx(req types.RequestCheckTx) types.ResponseCheckTx {
	return types.ResponseCheckTx{Code: code.CodeTypeOK, GasWanted: 1}
}

func (app *Application) Commit() types.ResponseCommit {
	// Using a memdb - just return the big endian size of the db
	appHash := make([]byte, 8)
	binary.PutVarint(appHash, app.state.Size)
	app.state.AppHash = appHash
	app.state.Height++
	saveState(app.state)

	resp := types.ResponseCommit{Data: appHash}
	if app.RetainBlocks > 0 && app.state.Height >= app.RetainBlocks {
		resp.RetainHeight = app.state.Height - app.RetainBlocks + 1
	}
	return resp
}

// Returns an associated value or nil if missing.
func (app *Application) Query(reqQuery types.RequestQuery) (resQuery types.ResponseQuery) {
	if reqQuery.Prove {
		value, err := app.state.db.Get(prefixKey(reqQuery.Data))
		if err != nil {
			panic(err)
		}
		if value == nil {
			resQuery.Log = "does not exist"
		} else {
			resQuery.Log = "exists"
		}
		resQuery.Index = -1 // TODO make Proof return index
		resQuery.Key = reqQuery.Data
		resQuery.Value = value
		resQuery.Height = app.state.Height

		return
	}

	resQuery.Key = reqQuery.Data
	value, err := app.state.db.Get(prefixKey(reqQuery.Data))
	if err != nil {
		panic(err)
	}
	if value == nil {
		resQuery.Log = "does not exist"
	} else {
		resQuery.Log = "exists"
	}
	resQuery.Value = value
	resQuery.Height = app.state.Height

	return resQuery
}

func (app *Application) PreprocessTxs(
	req types.RequestPreprocessTxs) types.ResponsePreprocessTxs {
	randTxs := generateRandTxs(32, 256)
	randMsgs := generateRandNamespacedRawData(32, nmt.DefaultNamespaceIDLen, 256)
	randMessages := toMessageSlice(randMsgs)
	return types.ResponsePreprocessTxs{Txs: append(req.Txs, randTxs...), Messages: &tmproto.Messages{MessagesList: randMessages}}
}

func generateRandTxs(num int, size int) [][]byte {
	randMsgs := generateRandNamespacedRawData(num, nmt.DefaultNamespaceIDLen, size)
	for _, msg := range randMsgs {
		copy(msg[:nmt.DefaultNamespaceIDLen], tmtypes.TxNamespaceID)
	}
	return randMsgs
}

func toMessageSlice(msgs [][]byte) []*tmproto.Message {
	res := make([]*tmproto.Message, len(msgs))
	for i := 0; i < len(msgs); i++ {
		res[i] = &tmproto.Message{NamespaceId: msgs[i][:nmt.DefaultNamespaceIDLen], Data: msgs[i][nmt.DefaultNamespaceIDLen:]}
	}
	return res
}

func generateRandNamespacedRawData(total int, nidSize int, leafSize int) [][]byte {
	data := make([][]byte, total)
	for i := 0; i < total; i++ {
		nid := make([]byte, nidSize)
		rand.Read(nid)
		data[i] = nid
	}
	sortByteArrays(data)
	for i := 0; i < total; i++ {
		d := make([]byte, leafSize)
		rand.Read(d)
		data[i] = append(data[i], d...)
	}

	return data
}

func sortByteArrays(src [][]byte) {
	sort.Slice(src, func(i, j int) bool { return bytes.Compare(src[i], src[j]) < 0 })
}
