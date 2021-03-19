package blockchain

import (
	bcproto "github.com/klyed/tendermint/proto/tendermint/blockchain"
	"github.com/klyed/tendermint/types"
)

const (
	MaxMsgSize = types.MaxBlockSizeBytes +
		bcproto.BlockResponseMessagePrefixSize +
		bcproto.BlockResponseMessageFieldKeySize
)
