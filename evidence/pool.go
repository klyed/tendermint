package evidence

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gogo/protobuf/proto"
	gogotypes "github.com/gogo/protobuf/types"
	"github.com/google/orderedcode"
	dbm "github.com/klyed/tm-db"

	clist "github.com/klyed/tendermint/libs/clist"
	"github.com/klyed/tendermint/libs/log"
	tmproto "github.com/klyed/tendermint/proto/tendermint/types"
	sm "github.com/klyed/tendermint/state"
	"github.com/klyed/tendermint/types"
)

const (
	// prefixes are unique across all tm db's
	prefixCommitted = int64(9)
	prefixPending   = int64(10)
)

// Pool maintains a pool of valid evidence to be broadcasted and committed
type Pool struct {
	logger log.Logger

	evidenceStore dbm.DB
	evidenceList  *clist.CList // concurrent linked-list of evidence
	evidenceSize  uint32       // amount of pending evidence

	// needed to load validators to verify evidence
	stateDB sm.Store
	// needed to load headers and commits to verify evidence
	blockStore BlockStore

	mtx sync.Mutex
	// latest state
	state sm.State
	// evidence from consensus is buffered to this slice, awaiting until the next height
	// before being flushed to the pool. This prevents broadcasting and proposing of
	// evidence before the height with which the evidence happened is finished.
	consensusBuffer []duplicateVoteSet

	pruningHeight int64
	pruningTime   time.Time
}

// NewPool creates an evidence pool. If using an existing evidence store,
// it will add all pending evidence to the concurrent list.
func NewPool(logger log.Logger, evidenceDB dbm.DB, stateDB sm.Store, blockStore BlockStore) (*Pool, error) {
	state, err := stateDB.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	pool := &Pool{
		stateDB:         stateDB,
		blockStore:      blockStore,
		state:           state,
		logger:          logger,
		evidenceStore:   evidenceDB,
		evidenceList:    clist.New(),
		consensusBuffer: make([]duplicateVoteSet, 0),
	}

	// If pending evidence already in db, in event of prior failure, then check
	// for expiration, update the size and load it back to the evidenceList.
	pool.pruningHeight, pool.pruningTime = pool.removeExpiredPendingEvidence()
	evList, _, err := pool.listEvidence(prefixPending, -1)
	if err != nil {
		return nil, err
	}

	atomic.StoreUint32(&pool.evidenceSize, uint32(len(evList)))

	for _, ev := range evList {
		pool.evidenceList.PushBack(ev)
	}

	return pool, nil
}

// PendingEvidence is used primarily as part of block proposal and returns up to
// maxNum of uncommitted evidence.
func (evpool *Pool) PendingEvidence(maxBytes int64) ([]types.Evidence, int64) {
	if evpool.Size() == 0 {
		return []types.Evidence{}, 0
	}

	evidence, size, err := evpool.listEvidence(prefixPending, maxBytes)
	if err != nil {
		evpool.logger.Error("failed to retrieve pending evidence", "err", err)
	}

	return evidence, size
}

// Update takes both the new state and the evidence committed at that height and performs
// the following operations:
// 1. Take any conflicting votes from consensus and use the state's LastBlockTime to form
//    DuplicateVoteEvidence and add it to the pool.
// 2. Update the pool's state which contains evidence params relating to expiry.
// 3. Moves pending evidence that has now been committed into the committed pool.
// 4. Removes any expired evidence based on both height and time.
func (evpool *Pool) Update(state sm.State, ev types.EvidenceList) {
	// sanity check
	if state.LastBlockHeight <= evpool.state.LastBlockHeight {
		panic(fmt.Sprintf(
			"failed EvidencePool.Update new state height is less than or equal to previous state height: %d <= %d",
			state.LastBlockHeight,
			evpool.state.LastBlockHeight,
		))
	}

	evpool.logger.Debug(
		"updating evidence pool",
		"last_block_height", state.LastBlockHeight,
		"last_block_time", state.LastBlockTime,
	)

	// flush conflicting vote pairs from the buffer, producing DuplicateVoteEvidence and
	// adding it to the pool
	evpool.processConsensusBuffer(state)
	// update state
	evpool.updateState(state)

	// move committed evidence out from the pending pool and into the committed pool
	evpool.markEvidenceAsCommitted(ev, state.LastBlockHeight)

	// Prune pending evidence when it has expired. This also updates when the next
	// evidence will expire.
	if evpool.Size() > 0 && state.LastBlockHeight > evpool.pruningHeight &&
		state.LastBlockTime.After(evpool.pruningTime) {
		evpool.pruningHeight, evpool.pruningTime = evpool.removeExpiredPendingEvidence()
	}
}

// AddEvidence checks the evidence is valid and adds it to the pool.
func (evpool *Pool) AddEvidence(ev types.Evidence) error {
	evpool.logger.Debug("attempting to add evidence", "evidence", ev)

	// We have already verified this piece of evidence - no need to do it again
	if evpool.isPending(ev) {
		evpool.logger.Debug("evidence already pending; ignoring", "evidence", ev)
		return nil
	}

	// check that the evidence isn't already committed
	if evpool.isCommitted(ev) {
		// This can happen if the peer that sent us the evidence is behind so we
		// shouldn't punish the peer.
		evpool.logger.Debug("evidence was already committed; ignoring", "evidence", ev)
		return nil
	}

	// 1) Verify against state.
	if err := evpool.verify(ev); err != nil {
		return err
	}

	// 2) Save to store.
	if err := evpool.addPendingEvidence(ev); err != nil {
		return fmt.Errorf("failed to add evidence to pending list: %w", err)
	}

	// 3) Add evidence to clist.
	evpool.evidenceList.PushBack(ev)

	evpool.logger.Info("verified new evidence of byzantine behavior", "evidence", ev)
	return nil
}

// ReportConflictingVotes takes two conflicting votes and forms duplicate vote evidence,
// adding it eventually to the evidence pool.
//
// Duplicate vote attacks happen before the block is committed and the timestamp is
// finalized, thus the evidence pool holds these votes in a buffer, forming the
// evidence from them once consensus at that height has been reached and `Update()` with
// the new state called.
//
// Votes are not verified.
func (evpool *Pool) ReportConflictingVotes(voteA, voteB *types.Vote) {
	evpool.mtx.Lock()
	defer evpool.mtx.Unlock()
	evpool.consensusBuffer = append(evpool.consensusBuffer, duplicateVoteSet{
		VoteA: voteA,
		VoteB: voteB,
	})
}

// CheckEvidence takes an array of evidence from a block and verifies all the evidence there.
// If it has already verified the evidence then it jumps to the next one. It ensures that no
// evidence has already been committed or is being proposed twice. It also adds any
// evidence that it doesn't currently have so that it can quickly form ABCI Evidence later.
func (evpool *Pool) CheckEvidence(evList types.EvidenceList) error {
	hashes := make([][]byte, len(evList))
	for idx, ev := range evList {

		ok := evpool.fastCheck(ev)

		if !ok {
			// check that the evidence isn't already committed
			if evpool.isCommitted(ev) {
				return &types.ErrInvalidEvidence{Evidence: ev, Reason: errors.New("evidence was already committed")}
			}

			err := evpool.verify(ev)
			if err != nil {
				return err
			}

			if err := evpool.addPendingEvidence(ev); err != nil {
				// Something went wrong with adding the evidence but we already know it is valid
				// hence we log an error and continue
				evpool.logger.Error("failed to add evidence to pending list", "err", err, "evidence", ev)
			}

			evpool.logger.Info("verified new evidence of byzantine behavior", "evidence", ev)
		}

		// check for duplicate evidence. We cache hashes so we don't have to work them out again.
		hashes[idx] = ev.Hash()
		for i := idx - 1; i >= 0; i-- {
			if bytes.Equal(hashes[i], hashes[idx]) {
				return &types.ErrInvalidEvidence{Evidence: ev, Reason: errors.New("duplicate evidence")}
			}
		}
	}

	return nil
}

// EvidenceFront goes to the first evidence in the clist
func (evpool *Pool) EvidenceFront() *clist.CElement {
	return evpool.evidenceList.Front()
}

// EvidenceWaitChan is a channel that closes once the first evidence in the list
// is there. i.e Front is not nil.
func (evpool *Pool) EvidenceWaitChan() <-chan struct{} {
	return evpool.evidenceList.WaitChan()
}

// Size returns the number of evidence in the pool.
func (evpool *Pool) Size() uint32 {
	return atomic.LoadUint32(&evpool.evidenceSize)
}

// State returns the current state of the evpool.
func (evpool *Pool) State() sm.State {
	evpool.mtx.Lock()
	defer evpool.mtx.Unlock()
	return evpool.state
}

// fastCheck leverages the fact that the evidence pool may have already verified
// the evidence to see if it can quickly conclude that the evidence is already
// valid.
func (evpool *Pool) fastCheck(ev types.Evidence) bool {
	if lcae, ok := ev.(*types.LightClientAttackEvidence); ok {
		key := keyPending(ev)
		evBytes, err := evpool.evidenceStore.Get(key)
		if evBytes == nil { // the evidence is not in the nodes pending list
			return false
		}

		if err != nil {
			evpool.logger.Error("failed to load light client attack evidence", "err", err, "key(height/hash)", key)
			return false
		}

		var trustedPb tmproto.LightClientAttackEvidence

		if err = trustedPb.Unmarshal(evBytes); err != nil {
			evpool.logger.Error(
				"failed to convert light client attack evidence from bytes",
				"key(height/hash)", key,
				"err", err,
			)
			return false
		}

		trustedEv, err := types.LightClientAttackEvidenceFromProto(&trustedPb)
		if err != nil {
			evpool.logger.Error(
				"failed to convert light client attack evidence from protobuf",
				"key(height/hash)", key,
				"err", err,
			)
			return false
		}

		// Ensure that all the byzantine validators that the evidence pool has match
		// the byzantine validators in this evidence.
		if trustedEv.ByzantineValidators == nil && lcae.ByzantineValidators != nil {
			return false
		}

		if len(trustedEv.ByzantineValidators) != len(lcae.ByzantineValidators) {
			return false
		}

		byzValsCopy := make([]*types.Validator, len(lcae.ByzantineValidators))
		for i, v := range lcae.ByzantineValidators {
			byzValsCopy[i] = v.Copy()
		}

		// ensure that both validator arrays are in the same order
		sort.Sort(types.ValidatorsByVotingPower(byzValsCopy))

		for idx, val := range trustedEv.ByzantineValidators {
			if !bytes.Equal(byzValsCopy[idx].Address, val.Address) {
				return false
			}
			if byzValsCopy[idx].VotingPower != val.VotingPower {
				return false
			}
		}

		return true
	}

	// For all other evidence the evidence pool just checks if it is already in
	// the pending db.
	return evpool.isPending(ev)
}

// IsExpired checks whether evidence or a polc is expired by checking whether a height and time is older
// than set by the evidence consensus parameters
func (evpool *Pool) isExpired(height int64, time time.Time) bool {
	var (
		params       = evpool.State().ConsensusParams.Evidence
		ageDuration  = evpool.State().LastBlockTime.Sub(time)
		ageNumBlocks = evpool.State().LastBlockHeight - height
	)
	return ageNumBlocks > params.MaxAgeNumBlocks &&
		ageDuration > params.MaxAgeDuration
}

// IsCommitted returns true if we have already seen this exact evidence and it is already marked as committed.
func (evpool *Pool) isCommitted(evidence types.Evidence) bool {
	key := keyCommitted(evidence)
	ok, err := evpool.evidenceStore.Has(key)
	if err != nil {
		evpool.logger.Error("failed to find committed evidence", "err", err)
	}
	return ok
}

// IsPending checks whether the evidence is already pending. DB errors are passed to the logger.
func (evpool *Pool) isPending(evidence types.Evidence) bool {
	key := keyPending(evidence)
	ok, err := evpool.evidenceStore.Has(key)
	if err != nil {
		evpool.logger.Error("failed to find pending evidence", "err", err)
	}
	return ok
}

func (evpool *Pool) addPendingEvidence(ev types.Evidence) error {
	evpb, err := types.EvidenceToProto(ev)
	if err != nil {
		return fmt.Errorf("failed to convert to proto: %w", err)
	}

	evBytes, err := evpb.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal evidence: %w", err)
	}

	key := keyPending(ev)

	err = evpool.evidenceStore.Set(key, evBytes)
	if err != nil {
		return fmt.Errorf("failed to persist evidence: %w", err)
	}

	atomic.AddUint32(&evpool.evidenceSize, 1)
	return nil
}

// markEvidenceAsCommitted processes all the evidence in the block, marking it as
// committed and removing it from the pending database.
func (evpool *Pool) markEvidenceAsCommitted(evidence types.EvidenceList, height int64) {
	blockEvidenceMap := make(map[string]struct{}, len(evidence))
	batch := evpool.evidenceStore.NewBatch()
	defer batch.Close()

	for _, ev := range evidence {
		if evpool.isPending(ev) {
			if err := batch.Delete(keyPending(ev)); err != nil {
				evpool.logger.Error("failed to batch pending evidence", "err", err)
			}
			blockEvidenceMap[evMapKey(ev)] = struct{}{}
		}

		// Add evidence to the committed list. As the evidence is stored in the block store
		// we only need to record the height that it was saved at.
		key := keyCommitted(ev)

		h := gogotypes.Int64Value{Value: height}
		evBytes, err := proto.Marshal(&h)
		if err != nil {
			evpool.logger.Error("failed to marshal committed evidence", "key(height/hash)", key, "err", err)
			continue
		}

		if err := evpool.evidenceStore.Set(key, evBytes); err != nil {
			evpool.logger.Error("failed to save committed evidence", "key(height/hash)", key, "err", err)
		}

		evpool.logger.Debug("marked evidence as committed", "evidence", ev)
	}

	// check if we need to remove any pending evidence
	if len(blockEvidenceMap) == 0 {
		return
	}

	// remove committed evidence from pending bucket
	if err := batch.WriteSync(); err != nil {
		evpool.logger.Error("failed to batch delete pending evidence", "err", err)
		return
	}

	// remove committed evidence from the clist
	evpool.removeEvidenceFromList(blockEvidenceMap)

	// update the evidence size
	atomic.AddUint32(&evpool.evidenceSize, ^uint32(len(blockEvidenceMap)-1))
}

// listEvidence retrieves lists evidence from oldest to newest within maxBytes.
// If maxBytes is -1, there's no cap on the size of returned evidence.
func (evpool *Pool) listEvidence(prefixKey int64, maxBytes int64) ([]types.Evidence, int64, error) {
	var (
		evSize    int64
		totalSize int64
		evidence  []types.Evidence
		evList    tmproto.EvidenceList // used for calculating the bytes size
	)

	iter, err := dbm.IteratePrefix(evpool.evidenceStore, prefixToBytes(prefixKey))
	if err != nil {
		return nil, totalSize, fmt.Errorf("database error: %v", err)
	}

	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		var evpb tmproto.Evidence

		if err := evpb.Unmarshal(iter.Value()); err != nil {
			return evidence, totalSize, err
		}

		evList.Evidence = append(evList.Evidence, evpb)
		evSize = int64(evList.Size())

		if maxBytes != -1 && evSize > maxBytes {
			if err := iter.Error(); err != nil {
				return evidence, totalSize, err
			}
			return evidence, totalSize, nil
		}

		ev, err := types.EvidenceFromProto(&evpb)
		if err != nil {
			return nil, totalSize, err
		}

		totalSize = evSize
		evidence = append(evidence, ev)
	}

	if err := iter.Error(); err != nil {
		return evidence, totalSize, err
	}

	return evidence, totalSize, nil
}

func (evpool *Pool) removeExpiredPendingEvidence() (int64, time.Time) {
	batch := evpool.evidenceStore.NewBatch()
	defer batch.Close()

	height, time, blockEvidenceMap := evpool.batchExpiredPendingEvidence(batch)

	// if we haven't removed any evidence then return early
	if len(blockEvidenceMap) == 0 {
		return height, time
	}

	evpool.logger.Debug("removing expired evidence",
		"height", evpool.State().LastBlockHeight,
		"time", evpool.State().LastBlockTime,
		"expired evidence", len(blockEvidenceMap),
	)

	// remove expired evidence from pending bucket
	if err := batch.WriteSync(); err != nil {
		evpool.logger.Error("failed to batch delete pending evidence", "err", err)
		return evpool.State().LastBlockHeight, evpool.State().LastBlockTime
	}

	// remove evidence from the clist
	evpool.removeEvidenceFromList(blockEvidenceMap)

	// update the evidence size
	atomic.AddUint32(&evpool.evidenceSize, ^uint32(len(blockEvidenceMap)-1))

	return height, time
}

func (evpool *Pool) batchExpiredPendingEvidence(batch dbm.Batch) (int64, time.Time, map[string]struct{}) {
	blockEvidenceMap := make(map[string]struct{})
	iter, err := dbm.IteratePrefix(evpool.evidenceStore, prefixToBytes(prefixPending))
	if err != nil {
		evpool.logger.Error("failed to iterate over pending evidence", "err", err)
		return evpool.State().LastBlockHeight, evpool.State().LastBlockTime, blockEvidenceMap
	}
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		ev, err := bytesToEv(iter.Value())
		if err != nil {
			evpool.logger.Error("failed to transition evidence from protobuf", "err", err, "ev", ev)
			continue
		}

		// if true, we have looped through all expired evidence
		if !evpool.isExpired(ev.Height(), ev.Time()) {
			// Return the height and time with which this evidence will have expired
			// so we know when to prune next.
			return ev.Height() + evpool.State().ConsensusParams.Evidence.MaxAgeNumBlocks + 1,
				ev.Time().Add(evpool.State().ConsensusParams.Evidence.MaxAgeDuration).Add(time.Second),
				blockEvidenceMap
		}

		// else add to the batch
		if err := batch.Delete(iter.Key()); err != nil {
			evpool.logger.Error("failed to batch evidence", "err", err, "ev", ev)
			continue
		}

		// and add to the map to remove the evidence from the clist
		blockEvidenceMap[evMapKey(ev)] = struct{}{}
	}

	return evpool.State().LastBlockHeight, evpool.State().LastBlockTime, blockEvidenceMap
}

func (evpool *Pool) removeEvidenceFromList(
	blockEvidenceMap map[string]struct{}) {

	for e := evpool.evidenceList.Front(); e != nil; e = e.Next() {
		// Remove from clist
		ev := e.Value.(types.Evidence)
		if _, ok := blockEvidenceMap[evMapKey(ev)]; ok {
			evpool.evidenceList.Remove(e)
			e.DetachPrev()
		}
	}
}

func (evpool *Pool) updateState(state sm.State) {
	evpool.mtx.Lock()
	defer evpool.mtx.Unlock()
	evpool.state = state
}

// processConsensusBuffer converts all the duplicate votes witnessed from consensus
// into DuplicateVoteEvidence. It sets the evidence timestamp to the block height
// from the most recently committed block.
// Evidence is then added to the pool so as to be ready to be broadcasted and proposed.
func (evpool *Pool) processConsensusBuffer(state sm.State) {
	evpool.mtx.Lock()
	defer evpool.mtx.Unlock()
	for _, voteSet := range evpool.consensusBuffer {

		// Check the height of the conflicting votes and fetch the corresponding time and validator set
		// to produce the valid evidence
		var dve *types.DuplicateVoteEvidence
		switch {
		case voteSet.VoteA.Height == state.LastBlockHeight:
			dve = types.NewDuplicateVoteEvidence(
				voteSet.VoteA,
				voteSet.VoteB,
				state.LastBlockTime,
				state.LastValidators,
			)

		case voteSet.VoteA.Height < state.LastBlockHeight:
			valSet, err := evpool.stateDB.LoadValidators(voteSet.VoteA.Height)
			if err != nil {
				evpool.logger.Error("failed to load validator set for conflicting votes",
					"height", voteSet.VoteA.Height, "err", err)
				continue
			}
			blockMeta := evpool.blockStore.LoadBlockMeta(voteSet.VoteA.Height)
			if blockMeta == nil {
				evpool.logger.Error("failed to load block time for conflicting votes", "height", voteSet.VoteA.Height)
				continue
			}
			dve = types.NewDuplicateVoteEvidence(
				voteSet.VoteA,
				voteSet.VoteB,
				blockMeta.Header.Time,
				valSet,
			)

		default:
			// evidence pool shouldn't expect to get votes from consensus of a height that is above the current
			// state. If this error is seen then perhaps consider keeping the votes in the buffer and retry
			// in following heights
			evpool.logger.Error("inbound duplicate votes from consensus are of a greater height than current state",
				"duplicate vote height", voteSet.VoteA.Height,
				"state.LastBlockHeight", state.LastBlockHeight)
			continue
		}

		// check if we already have this evidence
		if evpool.isPending(dve) {
			evpool.logger.Debug("evidence already pending; ignoring", "evidence", dve)
			continue
		}

		// check that the evidence is not already committed on chain
		if evpool.isCommitted(dve) {
			evpool.logger.Debug("evidence already committed; ignoring", "evidence", dve)
			continue
		}

		if err := evpool.addPendingEvidence(dve); err != nil {
			evpool.logger.Error("failed to flush evidence from consensus buffer to pending list: %w", err)
			continue
		}

		evpool.evidenceList.PushBack(dve)

		evpool.logger.Info("verified new evidence of byzantine behavior", "evidence", dve)
	}
	// reset consensus buffer
	evpool.consensusBuffer = make([]duplicateVoteSet, 0)
}

type duplicateVoteSet struct {
	VoteA *types.Vote
	VoteB *types.Vote
}

func bytesToEv(evBytes []byte) (types.Evidence, error) {
	var evpb tmproto.Evidence
	err := evpb.Unmarshal(evBytes)
	if err != nil {
		return &types.DuplicateVoteEvidence{}, err
	}

	return types.EvidenceFromProto(&evpb)
}

func evMapKey(ev types.Evidence) string {
	return string(ev.Hash())
}

func prefixToBytes(prefix int64) []byte {
	key, err := orderedcode.Append(nil, prefix)
	if err != nil {
		panic(err)
	}
	return key
}

func keyCommitted(evidence types.Evidence) []byte {
	var height int64 = evidence.Height()
	key, err := orderedcode.Append(nil, prefixCommitted, height, string(evidence.Hash()))
	if err != nil {
		panic(err)
	}
	return key
}

func keyPending(evidence types.Evidence) []byte {
	var height int64 = evidence.Height()
	key, err := orderedcode.Append(nil, prefixPending, height, string(evidence.Hash()))
	if err != nil {
		panic(err)
	}
	return key
}
