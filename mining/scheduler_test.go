package mining

import (
	"context"
	"testing"
	"time"

	"github.com/filecoin-project/go-filecoin/consensus"
	th "github.com/filecoin-project/go-filecoin/testhelpers"
	"github.com/filecoin-project/go-filecoin/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestUtils(t *testing.T) (*assert.Assertions, *require.Assertions, consensus.TipSet) {
	assert := assert.New(t)
	require := require.New(t)
	baseBlock := &types.Block{StateRoot: types.SomeCid()}

	ts := consensus.TipSet{baseBlock.Cid().String(): baseBlock}
	return assert, require, ts
}

// TestMineOnce tests that the MineOnce function results in a mining job being
// scheduled and run by the mining scheduler.
func TestMineOnce(t *testing.T) {
	assert, require, ts := newTestUtils(t)

	// Echoes the sent block to output.
	worker := NewTestWorkerWithDeps(MakeEchoMine(require))
	result := MineOnce(context.Background(), worker, MineDelayTest, ts)
	assert.NoError(result.Err)
	assert.True(ts.ToSlice()[0].StateRoot.Equals(result.NewBlock.StateRoot))
}

func TestSchedulerPassesValue(t *testing.T) {
	assert, _, ts := newTestUtils(t)
	ctx, cancel := context.WithCancel(context.Background())

	checkValsMine := func(c context.Context, inTS consensus.TipSet, nBC int, outCh chan<- Output) {
		assert.Equal(ctx, c) // individual run ctx splits off from mining ctx
		assert.Equal(inTS, ts)
		outCh <- Output{}
	}
	var head consensus.TipSet
	headFunc := func() consensus.TipSet {
		return head
	}
	worker := NewTestWorkerWithDeps(checkValsMine)
	scheduler := NewScheduler(worker, MineDelayTest, headFunc)
	head = ts // set head so headFunc returns correctly
	outCh, _ := scheduler.Start(ctx)
	<-outCh
	cancel()
}

func TestSchedulerErrorsOnUnsetHead(t *testing.T) {
	assert, _, _ := newTestUtils(t)
	ctx := context.Background()

	nothingMine := func(c context.Context, inTS consensus.TipSet, nBC int, outCh chan<- Output) {
		outCh <- Output{}
	}
	nilHeadFunc := func() consensus.TipSet {
		return nil
	}
	worker := NewTestWorkerWithDeps(nothingMine)
	scheduler := NewScheduler(worker, MineDelayTest, nilHeadFunc)
	outCh, doneWg := scheduler.Start(ctx)
	output := <-outCh
	assert.Error(output.Err)
	doneWg.Wait()
}

// If head is the same increment the nullblkcount, otherwise make it 0.
func TestSchedulerUpdatesNullBlkCount(t *testing.T) {
	assert, require, ts := newTestUtils(t)
	ctx, cancel := context.WithCancel(context.Background())
	blk2 := &types.Block{StateRoot: types.SomeCid(), Height: 1}
	ts2 := consensus.RequireNewTipSet(require, blk2)

	checkNullBlocks := 0
	checkNullBlockMine := func(c context.Context, inTS consensus.TipSet, nBC int, outCh chan<- Output) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		assert.Equal(checkNullBlocks, nBC)
		outCh <- Output{}
	}
	var head consensus.TipSet
	headFunc := func() consensus.TipSet {
		return head
	}
	worker := NewTestWorkerWithDeps(checkNullBlockMine)
	scheduler := NewScheduler(worker, MineDelayTest, headFunc)
	head = ts
	outCh, _ := scheduler.Start(ctx)
	<-outCh
	// setting checkNullBlocks races with the mining delay timer.
	checkNullBlocks = 1
	<-outCh
	checkNullBlocks = 2
	<-outCh
	head = ts2
	checkNullBlocks = 0
	<-outCh
	cancel()
}

// Test that we can push multiple blocks through.  This schedules tipsets
// with successively higher block heights (aka epoch).
func TestSchedulerPassesManyValues(t *testing.T) {
	assert, require, ts1 := newTestUtils(t)
	ctx, cancel := context.WithCancel(context.Background())
	var checkTS consensus.TipSet
	// make tipsets with progressively higher heights
	blk2 := &types.Block{StateRoot: types.SomeCid(), Height: 1}
	ts2 := consensus.RequireNewTipSet(require, blk2)
	blk3 := &types.Block{StateRoot: types.SomeCid(), Height: 2}
	ts3 := consensus.RequireNewTipSet(require, blk3)
	var head consensus.TipSet
	headFunc := func() consensus.TipSet {
		return head
	}

	checkValsMine := func(c context.Context, ts consensus.TipSet, nBC int, outCh chan<- Output) {
		assert.Equal(ts, checkTS)
		outCh <- Output{}
	}
	worker := NewTestWorkerWithDeps(checkValsMine)
	scheduler := NewScheduler(worker, MineDelayTest, headFunc)
	checkTS = ts1
	head = ts1
	outCh, _ := scheduler.Start(ctx)
	<-outCh
	// This is testing a race (that checkTS and head are both set before
	// the headFunc is called, but the TestMine delay should be long enough
	// that it should work.  TODO: eliminate races.
	checkTS = ts2
	head = ts2
	<-outCh
	checkTS = ts3 // Same race as ^^
	head = ts3
	<-outCh
	cancel()
}

// TestSchedulerCollect tests that the scheduler collects tipsets before mining
func TestSchedulerCollect(t *testing.T) {
	assert, require, ts1 := newTestUtils(t)
	ctx, cancel := context.WithCancel(context.Background())
	blk2 := &types.Block{StateRoot: types.SomeCid(), Height: 1}
	ts2 := consensus.RequireNewTipSet(require, blk2)
	blk3 := &types.Block{StateRoot: types.SomeCid(), Height: 1}
	ts3 := consensus.RequireNewTipSet(require, blk3)
	var head consensus.TipSet
	headFunc := func() consensus.TipSet {
		return head
	}
	checkValsMine := func(c context.Context, inTS consensus.TipSet, nBC int, outCh chan<- Output) {
		assert.Equal(inTS, ts3)
		outCh <- Output{}
	}
	worker := NewTestWorkerWithDeps(checkValsMine)
	scheduler := NewScheduler(worker, MineDelayTest, headFunc)
	head = ts1
	outCh, _ := scheduler.Start(ctx)
	// again this is racing on the assumption that mining delay is long
	// enough for all these variables to be set before the sleep finishes.
	head = ts2
	head = ts3 // the scheduler should collect the latest input
	<-outCh
	cancel()
}

// This test is no longer meaningful without mocking ticket generation winning.
// We need some way to make sure that the block being mined is still the block
// received during collect.  TODO: isWinningTicket faking and reimplementing
// in this new paradigm
/*
func TestCannotInterruptMiner(t *testing.T) {
	assert, require, ts1 := newTestUtils(t)
	ctx, cancel := context.WithCancel(context.Background())
	blk1 := ts1.ToSlice()[0]
	blk2 := &types.Block{StateRoot: types.SomeCid(), Height: 0}
	ts2 := consensus.RequireNewTipSet(require, blk2)
	blockingMine := func(c context.Context, ts consensus.TipSet, nBC int, outCh chan<- Output) {
		time.Sleep(th.BlockTimeTest)
		assert.Equal(ts, ts1)
		outCh <- Output{NewBlock: blk1}
	}
	var head consensus.TipSet
	headFunc := func() consensus.TipSet {
		return head
	}
	worker := NewTestWorkerWithDeps(blockingMine)
	scheduler := NewScheduler(worker, MineDelayTest)
	inCh, outCh, _ := scheduler.Start(ctx)
	inCh <- NewInput(ts1)
	// Wait until well after the mining delay, and send a new input.
	time.Sleep(4 * MineDelayTest)
	inCh <- NewInput(ts2)
	out := <-outCh
	assert.Equal(out.NewBlock, blk1)
	cancel()
}*/

func TestSchedulerCancelMiningCtx(t *testing.T) {
	assert, _, ts := newTestUtils(t)
	// Test that canceling the mining context stops mining, cancels
	// the inner context, and closes the output channel.
	miningCtx, miningCtxCancel := context.WithCancel(context.Background())
	var head consensus.TipSet
	headFunc := func() consensus.TipSet {
		return head
	}
	shouldCancelMine := func(c context.Context, inTS consensus.TipSet, nBC int, outCh chan<- Output) {
		mineTimer := time.NewTimer(th.BlockTimeTest)
		select {
		case <-mineTimer.C:
			t.Fatal("should not take whole time")
		case <-c.Done():
		}
	}
	worker := NewTestWorkerWithDeps(shouldCancelMine)
	scheduler := NewScheduler(worker, MineDelayTest, headFunc)
	head = ts
	outCh, doneWg := scheduler.Start(miningCtx)
	miningCtxCancel()
	doneWg.Wait()
	assert.Equal(ChannelClosed, ReceiveOutCh(outCh))
}

func TestSchedulerMultiRoundWithCollect(t *testing.T) {
	assert, require, ts1 := newTestUtils(t)
	ctx, cancel := context.WithCancel(context.Background())
	var checkTS consensus.TipSet
	var head consensus.TipSet
	headFunc := func() consensus.TipSet {
		return head
	}
	// make tipsets with progressively higher heights
	blk2 := &types.Block{StateRoot: types.SomeCid(), Height: 1}
	ts2 := consensus.RequireNewTipSet(require, blk2)
	blk3 := &types.Block{StateRoot: types.SomeCid(), Height: 2}
	ts3 := consensus.RequireNewTipSet(require, blk3)

	checkValsMine := func(c context.Context, inTS consensus.TipSet, nBC int, outCh chan<- Output) {
		assert.Equal(inTS, checkTS)
		outCh <- Output{}
		// two outputs, to allow us to change values before it runs again without racing
		outCh <- Output{}
	}
	worker := NewTestWorkerWithDeps(checkValsMine)
	scheduler := NewScheduler(worker, MineDelayTest, headFunc)
	checkTS = ts1
	head = ts1
	outCh, doneWg := scheduler.Start(ctx)

	<-outCh
	head = ts2 // again we're racing :(
	checkTS = ts2
	<-outCh

	<-outCh
	checkTS = ts3
	head = ts3
	<-outCh

	cancel()
	doneWg.Wait()
	assert.Equal(ChannelClosed, ReceiveOutCh(outCh))
}
