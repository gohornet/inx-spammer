package pow

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	"github.com/iotaledger/hive.go/contextutils"
	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/inx-spammer/pkg/common"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/pow"
)

const (
	nonceBytes = 8 // len(uint64)
)

// RefreshTipsFunc refreshes tips of the block if PoW takes longer than a configured duration.
type RefreshTipsFunc = func() (tips iotago.BlockIDs, err error)

// DoPoW does the proof-of-work required to hit the given target score.
// The given iota.Block's nonce is automatically updated.
func DoPoW(ctx context.Context, block *iotago.Block, targetScore float64, parallelism int, refreshTipsInterval time.Duration, refreshTipsFunc RefreshTipsFunc) (blockSize int, err error) {

	if err := contextutils.ReturnErrIfCtxDone(ctx, common.ErrOperationAborted); err != nil {
		return 0, err
	}

	getPoWData := func(block *iotago.Block) (powData []byte, err error) {
		blockData, err := block.Serialize(serializer.DeSeriModeNoValidation, nil)
		if err != nil {
			return nil, fmt.Errorf("unable to perform PoW as block can't be serialized: %w", err)
		}

		return blockData[:len(blockData)-nonceBytes], nil
	}

	powData, err := getPoWData(block)
	if err != nil {
		return 0, err
	}

	doPow := func(ctx context.Context) (uint64, error) {
		powCtx, powCancel := context.WithCancel(ctx)
		defer powCancel()

		if refreshTipsFunc != nil {
			var powTimeoutCancel context.CancelFunc
			powCtx, powTimeoutCancel = context.WithTimeout(powCtx, refreshTipsInterval)
			defer powTimeoutCancel()
		}

		nonce, err := pow.New(parallelism).Mine(powCtx, powData, targetScore)
		if err != nil {
			if errors.Is(err, pow.ErrCancelled) && refreshTipsFunc != nil {
				// context was canceled and tips can be refreshed
				tips, err := refreshTipsFunc()
				if err != nil {
					return 0, err
				}
				block.Parents = tips

				// replace the powData to update the new tips
				powData, err = getPoWData(block)
				if err != nil {
					return 0, err
				}

				return 0, pow.ErrCancelled
			}
			return 0, err
		}

		return nonce, nil
	}

	for {
		nonce, err := doPow(ctx)
		if err != nil {
			// check if the external context got canceled.
			if ctx.Err() != nil {
				return 0, common.ErrOperationAborted
			}

			if errors.Is(err, pow.ErrCancelled) {
				// redo the PoW with new tips
				continue
			}
			return 0, err
		}

		block.Nonce = nonce
		return len(powData) + nonceBytes, nil
	}
}
