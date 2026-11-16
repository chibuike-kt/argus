package hyperliquid

// wireCandle is one candle as Hyperliquid's own API represents it --
// identical schema on both the WebSocket `candle` message and the REST
// `candleSnapshot` response (hyperliquid.gitbook.io/hyperliquid-docs/
// for-developers/api/websocket/subscriptions and .../info-endpoint,
// both read directly). There is deliberately no closed/final field:
// Hyperliquid's own schema does not have one.
type wireCandle struct {
	T  int64  `json:"t"` // open time, epoch milliseconds
	C  int64  `json:"T"` // close time, epoch milliseconds
	S  string `json:"s"` // coin
	I  string `json:"i"` // interval
	O  string `json:"o"` // open
	H  string `json:"h"` // high
	L  string `json:"l"` // low
	Cl string `json:"c"` // close
	V  string `json:"v"` // volume, base units
	N  int64  `json:"n"` // trade count
}

// wsSubscribeMessage is the client-to-server subscribe frame
// (hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/websocket/subscriptions).
type wsSubscribeMessage struct {
	Method       string         `json:"method"`
	Subscription wsSubscription `json:"subscription"`
}

type wsSubscription struct {
	Type     string `json:"type"`
	Coin     string `json:"coin"`
	Interval string `json:"interval"`
}

// metaRequest is the POST /info body for {"type":"meta"}.
type metaRequest struct {
	Type string `json:"type"`
}

// metaResponse is the perpetuals metadata response -- only the fields
// this adapter actually reads (szDecimals, name, maxLeverage) are
// modeled; the rest of Hyperliquid's own response is ignored, not
// erased -- an unknown field in the real response is simply not
// unmarshaled into anything, never a parse error (encoding/json's own
// default behavior).
type metaResponse struct {
	Universe []metaAsset `json:"universe"`
}

type metaAsset struct {
	Name        string `json:"name"`
	SzDecimals  int32  `json:"szDecimals"`
	MaxLeverage int64  `json:"maxLeverage"`
	IsDelisted  bool   `json:"isDelisted"`
}

// candleSnapshotRequest is the POST /info body for
// {"type":"candleSnapshot","req":{...}}.
type candleSnapshotRequest struct {
	Type string            `json:"type"`
	Req  candleSnapshotReq `json:"req"`
}

type candleSnapshotReq struct {
	Coin      string `json:"coin"`
	Interval  string `json:"interval"`
	StartTime int64  `json:"startTime"` // epoch milliseconds
	EndTime   int64  `json:"endTime"`   // epoch milliseconds
}
