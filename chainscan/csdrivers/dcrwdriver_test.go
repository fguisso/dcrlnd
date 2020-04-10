package csdrivers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"decred.org/dcrwallet/rpc/walletrpc"
	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/chaincfg/v2"
	"github.com/decred/dcrd/gcs/v2"
	"github.com/decred/dcrd/rpctest"
	"github.com/decred/dcrlnd/chainscan"
	"github.com/decred/dcrlnd/internal/testutils"
	"github.com/decred/dcrlnd/lntest/wait"
)

var (
	testHDSeed = chainhash.Hash{
		0xb7, 0x94, 0x38, 0x5f, 0x2d, 0x1e, 0xf7, 0xab,
		0x4d, 0x92, 0x73, 0xd1, 0x90, 0x63, 0x81, 0xb4,
		0x4f, 0x2f, 0x6f, 0x25, 0x98, 0xa3, 0xef, 0xb9,
		0x69, 0x49, 0x18, 0x83, 0x31, 0x98, 0x47, 0x53,
	}

	defaultTimeout = 5 * time.Second
)

type runnable interface {
	Run(context.Context) error
}

type testHarness struct {
	testutils.TB

	d     interface{} // driver
	miner *rpctest.Harness
	vw    *rpctest.VotingWallet
}

func (t *testHarness) generate(nb uint32) []*chainhash.Hash {
	t.Helper()

	bls, err := t.vw.GenerateBlocks(nb)
	if err != nil {
		t.Fatalf("unable to generate %d blocks: %v", nb, err)
	}
	return bls
}

// assertMinerBlockHeightDelta ensures that tempMiner is 'delta' blocks ahead
// of miner.
func assertMinerBlockHeightDelta(t *testHarness,
	miner, tempMiner *rpctest.Harness, delta int64) {

	// Ensure the chain lengths are what we expect.
	var predErr error
	err := wait.Predicate(func() bool {
		_, tempMinerHeight, err := tempMiner.Node.GetBestBlock()
		if err != nil {
			predErr = fmt.Errorf("unable to get current "+
				"blockheight %v", err)
			return false
		}

		_, minerHeight, err := miner.Node.GetBestBlock()
		if err != nil {
			predErr = fmt.Errorf("unable to get current "+
				"blockheight %v", err)
			return false
		}

		if tempMinerHeight != minerHeight+delta {
			predErr = fmt.Errorf("expected new miner(%d) to be %d "+
				"blocks ahead of original miner(%d)",
				tempMinerHeight, delta, minerHeight)
			return false
		}
		return true
	}, time.Second*15)
	if err != nil {
		t.Fatalf(predErr.Error())
	}
}

func assertMatchesMinerCF(t *testHarness, bh *chainhash.Hash, key [16]byte, filter *gcs.FilterV2) {
	t.Helper()

	resp, err := t.miner.Node.GetCFilterV2(bh)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resp.Filter.Bytes(), filter.Bytes()) {
		t.Fatal("filter bytes do not match")
	}
	mbl, err := t.miner.Node.GetBlock(bh)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mbl.Header.MerkleRoot[:16], key[:]) {
		t.Fatal("key does not match")
	}
}

func testCurrentTip(t *testHarness) {
	d := t.d.(chainscan.ChainSource)

	// Generate 5 blocks. The tip should match in every one.
	for i := 0; i < 5; i++ {
		err := wait.NoError(func() error {
			ctxt, cancel := context.WithTimeout(context.Background(), defaultTimeout)
			defer cancel()
			bh, h, err := d.CurrentTip(ctxt)
			if err != nil {
				return fmt.Errorf("unable to get current tip: %v", err)
			}

			// Compare to current miner tip.
			hash, height, err := t.miner.Node.GetBestBlock()
			if err != nil {
				return fmt.Errorf("unable to get best block: %v", err)
			}
			if int32(height) != h {
				return fmt.Errorf("unexpected tip height. want=%d got=%d", height, h)
			}
			if *bh != *hash {
				return fmt.Errorf("unexpected tip hash. want=%s got=%s", hash, bh)
			}

			return nil
		}, defaultTimeout)
		if err != nil {
			t.Fatal(err)
		}

		if i < 4 {
			t.generate(1)
		}
	}
}

func testGetCFilters(t *testHarness) {
	d := t.d.(chainscan.HistoricalChainSource)

	// Fetch a bunch of cfilters and compare it to the miner returned ones.
	_, tipHeight, err := t.miner.Node.GetBestBlock()
	if err != nil {
		t.Fatal(err)
	}

	for height := tipHeight - 10; height <= tipHeight; height++ {
		ctxt, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		bh, key, filter, err := d.GetCFilter(ctxt, int32(height))
		if err != nil {
			t.Fatalf("unable to get cfilter: %v", err)
		}

		// Compare to the miner.
		mbh, err := t.miner.Node.GetBlockHash(height)
		if err != nil {
			t.Fatal(err)
		}
		if *mbh != *bh {
			t.Fatalf("unexpected block hash at height %d. want=%s got=%s",
				height, mbh, bh)
		}
		assertMatchesMinerCF(t, bh, key, filter)
	}

	// Requesting a cfilter for a block past tip should return
	// ErrBlockAfterTip.
	ctxt, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	_, _, _, err = d.GetCFilter(ctxt, int32(tipHeight+1))
	if !errors.Is(err, chainscan.ErrBlockAfterTip{}) {
		t.Fatalf("unexpected error at tipHeight+1. want=%v got=%v",
			chainscan.ErrBlockAfterTip{}, err)
	}

	// Requesting a cfilter for tip again shouldn't error.
	ctxt, cancel = context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	_, _, _, err = d.GetCFilter(ctxt, int32(tipHeight))
	if err != nil {
		t.Fatalf("unexpected error at tipHeight. want=%v got=%v",
			nil, err)
	}
}

func testGetBlock(t *testHarness) {
	d := t.d.(chainscan.ChainSource)

	_, tipHeight, err := t.miner.Node.GetBestBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Fetch a bunch of blocks near tipHeight and ensure they match the
	// ones from the miner.
	for height := tipHeight - 5; height <= tipHeight; height++ {
		mbh, err := t.miner.Node.GetBlockHash(height)
		if err != nil {
			t.Fatal(err)
		}

		mbl, err := t.miner.Node.GetBlock(mbh)
		if err != nil {
			t.Fatal(err)
		}

		ctxt, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		bl, err := d.GetBlock(ctxt, mbh)
		if err != nil {
			t.Fatalf("unable to get block: %v", err)
		}

		blBytes, err := bl.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		mblBytes, err := mbl.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(blBytes, mblBytes) {
			t.Fatalf("bytes from miner block do not equal bytes from driver block")
		}
	}
}

func testChainEvents(t *testHarness) {
	d := t.d.(chainscan.TipChainSource)
	if r, isRunnable := t.d.(runnable); isRunnable {
		runCtx, cancelRun := context.WithCancel(context.Background())
		go r.Run(runCtx)
		defer cancelRun()
	}

	_, tipHeight, err := t.miner.Node.GetBestBlock()
	if err != nil {
		t.Fatal(err)
	}

	var (
		bh     *chainhash.Hash
		height int32
		key    [16]byte
		filter *gcs.FilterV2
	)

	eventsCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := d.ChainEvents(eventsCtx)

	// Repeat the test 5 times.
	for i := int32(0); i < 5; i++ {
		mbh := t.generate(1)[0]
		select {
		case ce := <-events:
			e := ce.(chainscan.BlockConnectedEvent)
			bh, height = e.BlockHash(), e.BlockHeight()
			key, filter = e.CFKey, e.Filter
		case <-time.After(defaultTimeout):
			t.Fatalf("timeout waiting for block %d", i)
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if *bh != *mbh {
			t.Fatalf("unexpected block hash. want=%s got=%s",
				mbh, bh)
		}
		if height != int32(tipHeight)+i+1 {
			t.Fatalf("unexpected height. want=%d got=%d",
				int32(tipHeight)+i+1, height)
		}

		assertMatchesMinerCF(t, bh, key, filter)
	}
}

// tests that using nextTip() when a reorg happens makes the driver get all new
// (reorged in) blocks.
func testChainEventsWithReorg(t *testHarness) {
	d := t.d.(chainscan.TipChainSource)
	if r, isRunnable := t.d.(runnable); isRunnable {
		runCtx, cancelRun := context.WithCancel(context.Background())
		go r.Run(runCtx)
		defer cancelRun()
	}

	_, tipHeight, err := t.miner.Node.GetBestBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Create a second miner.
	netParams := chaincfg.SimNetParams()
	tempMinerDir := fmt.Sprintf(".dcrd-alt-miner")
	tempMinerArgs := []string{"--debuglevel=debug", "--logdir=" + tempMinerDir}
	tempMiner, err := rpctest.New(netParams, nil, tempMinerArgs)
	if err != nil {
		t.Fatal(err)
	}
	err = tempMiner.SetUp(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tempMiner.TearDown()

	// Connect the temp miner with the orignal test miner and let them sync
	// up.
	if err := rpctest.ConnectNode(t.miner, tempMiner); err != nil {
		t.Fatalf("unable to connect harnesses: %v", err)
	}
	nodeSlice := []*rpctest.Harness{t.miner, tempMiner}
	if err := rpctest.JoinNodes(nodeSlice, rpctest.Blocks); err != nil {
		t.Fatalf("unable to join node on blocks: %v", err)
	}

	// The two miners should be on the same blockheight.
	assertMinerBlockHeightDelta(t, t.miner, tempMiner, 0)

	// Disconnect both nodes.
	err = rpctest.RemoveNode(t.miner, tempMiner)
	if err != nil {
		t.Fatalf("unable to remove node: %v", err)
	}

	// Create the chain events channel.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := d.ChainEvents(ctx)

	// Mine 3 blocks in the original miner and 6 in the temp miner.
	t.generate(3)
	_, err = testutils.AdjustedSimnetMiner(tempMiner.Node, 6)
	if err != nil {
		t.Fatal(err)
	}

	// The two miners should be on different blockheights.
	assertMinerBlockHeightDelta(t, t.miner, tempMiner, 3)

	var (
		bh     *chainhash.Hash
		height int32
		key    [16]byte
		filter *gcs.FilterV2
	)

	// We should get 3 new tips when calling NextTip()
	for i := int32(0); i < 3; i++ {
		select {
		case ce := <-events:
			e := ce.(chainscan.BlockConnectedEvent)
			bh, height = e.BlockHash(), e.BlockHeight()
			key, filter = e.CFKey, e.Filter
		case <-time.After(defaultTimeout):
			t.Fatalf("timeout waiting for block %d", i)
		}

		if height != int32(tipHeight)+i+1 {
			t.Fatalf("unexpected height. want=%d got=%d",
				int32(tipHeight)+i+1, height)
		}

		assertMatchesMinerCF(t, bh, key, filter)
	}

	// Re-connect the miners. This should cause 6 news blocks to be
	// connected (including ones for heights the we already just checked
	// were connected).
	if err := rpctest.ConnectNode(t.miner, tempMiner); err != nil {
		t.Fatalf("unable to connect harnesses: %v", err)
	}
	if err := rpctest.JoinNodes(nodeSlice, rpctest.Blocks); err != nil {
		t.Fatalf("unable to join node on blocks: %v", err)
	}
	assertMinerBlockHeightDelta(t, t.miner, tempMiner, 0)

	// We should get 3 BlockDisconnected events from the old chain.
	for i := int32(0); i < 3; i++ {
		select {
		case ce := <-events:
			e := ce.(chainscan.BlockDisconnectedEvent)
			_, height = e.BlockHash(), e.BlockHeight()
			wantHeight := int32(tipHeight) + 3 - i
			if height != wantHeight {
				t.Fatalf("unexpected BlockDisconnectedEvent "+
					"height. want=%d got=%d", wantHeight,
					height)
			}
		case <-time.After(defaultTimeout):
			t.Fatalf("timeout waiting for block disconnect %d", i)
		}
	}

	// We should get 6 new tips when calling NextTip()
	for i := int32(0); i < 6; i++ {
		select {
		case ce := <-events:
			e := ce.(chainscan.BlockConnectedEvent)
			bh, height = e.BlockHash(), e.BlockHeight()
			key, filter = e.CFKey, e.Filter
		case <-time.After(defaultTimeout):
			t.Fatalf("timeout waiting for block %d", i)
		}

		// This is the important bit of this test. We've never reset
		// tipHeight, therefore we should obverve again
		// tipHeight+1..tipHeight+1+3.
		if height != int32(tipHeight)+i+1 {
			t.Fatalf("unexpected height. want=%d got=%d",
				int32(tipHeight)+i+1, height)
		}

		assertMatchesMinerCF(t, bh, key, filter)
	}
}

func setupTestChain(t testutils.TB, testName string) (*rpctest.Harness, *rpctest.VotingWallet, func()) {
	tearDown := func() {}
	defer func() {
		if t.Failed() {
			tearDown()
		}
	}()

	netParams := chaincfg.SimNetParams()
	minerLogDir := fmt.Sprintf(".dcrd-%s", testName)
	minerArgs := []string{"--debuglevel=debug", "--logdir=" + minerLogDir}
	miner, err := rpctest.New(netParams, nil, minerArgs)
	if err != nil {
		t.Fatal(err)
	}
	err = miner.SetUp(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	tearDown = func() {
		miner.TearDown()
	}

	_, err = miner.Node.Generate(1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = testutils.AdjustedSimnetMiner(miner.Node, 64)
	if err != nil {
		t.Fatal(err)
	}

	// Setup a voting wallet for when the chain passes SVH.
	vw, err := rpctest.NewVotingWallet(miner)
	if err != nil {
		t.Fatalf("unable to create voting wallet: %v", err)
	}
	vw.SetErrorReporting(func(err error) {
		t.Logf("Voting wallet error: %v", err)
	})
	vw.SetMiner(func(nb uint32) ([]*chainhash.Hash, error) {
		return testutils.AdjustedSimnetMiner(miner.Node, nb)
	})
	if err = vw.Start(); err != nil {
		t.Fatalf("unable to start voting wallet: %v", err)
	}
	tearDown = func() {
		vw.Stop()
		miner.TearDown()
	}

	return miner, vw, tearDown
}

type testCase struct {
	name string
	f    func(*testHarness)
}

var testCases = []testCase{
	// The reorg test needs to be the first one to ensure voting
	// doesn't need to be taken into account.
	{
		name: "ChainEvents with reorg",
		f:    testChainEventsWithReorg,
	},
	{
		name: "CurrentTip",
		f:    testCurrentTip,
	},
	{
		name: "GetCFilters",
		f:    testGetCFilters,
	},
	{
		name: "GetBlock",
		f:    testGetBlock,
	},
	{
		name: "ChainEvents",
		f:    testChainEvents,
	},
}

func TestDcrwalletCSDriver(t *testing.T) {
	miner, vw, tearDownMiner := setupTestChain(t, "dcwallet-csd")
	defer tearDownMiner()

	rpcConfig := miner.RPCConfig()
	w, tearDownWallet := testutils.NewSyncingTestWallet(t, &rpcConfig)
	defer tearDownWallet()

	for _, tc := range testCases {
		tc := tc
		succ := t.Run(tc.name, func(t *testing.T) {
			d := NewDcrwalletCSDriver(w)

			// Lower the cache size so we're sure to trigger cases
			// where the cache is both used and filled.
			d.cache = make([]cfilter, 3)

			th := &testHarness{
				d:     d,
				TB:    t,
				miner: miner,
				vw:    vw,
			}
			tc.f(th)
		})
		if !succ {
			break
		}
	}
}

func TestRemoteDcrwalletCSDriver(t *testing.T) {
	miner, vw, tearDownMiner := setupTestChain(t, "remotewallet-csd")
	defer tearDownMiner()

	rpcConfig := miner.RPCConfig()
	conn, tearDownWallet := testutils.NewTestRemoteDcrwallet(t, &rpcConfig)
	wsvc := walletrpc.NewWalletServiceClient(conn)
	nsvc := walletrpc.NewNetworkServiceClient(conn)
	defer tearDownWallet()

	for _, tc := range testCases {
		tc := tc
		succ := t.Run(tc.name, func(t *testing.T) {
			d := NewRemoteWalletCSDriver(wsvc, nsvc)

			// Lower the cache size so we're sure to trigger cases
			// where the cache is both used and filled.
			d.cache = make([]cfilter, 3)

			th := &testHarness{
				d:     d,
				TB:    t,
				miner: miner,
				vw:    vw,
			}
			tc.f(th)
		})
		if !succ {
			break
		}
	}
}

// BenchmarkDcrwalletCSDriver benchmarks a series of GetCFilter calls.
//
// This ends up mostly testing your IO performnace. Note that you might want to
// run with `-benchtime=500x` to prevent the benchmark runtime from generating
// a large N (and therefore a large chain).
func BenchmarkDcrwalletCSDriver(b *testing.B) {
	miner, vw, tearDownMiner := setupTestChain(b, "dcrwallet-bench-csd")
	defer tearDownMiner()

	rpcConfig := miner.RPCConfig()
	w, tearDownWallet := testutils.NewSyncingTestWallet(b, &rpcConfig)
	defer tearDownWallet()

	d := NewDcrwalletCSDriver(w)
	th := &testHarness{
		d:     d,
		TB:    b,
		miner: miner,
		vw:    vw,
	}

	_, tipHeight, err := th.miner.Node.GetBestBlock()
	if err != nil {
		th.Fatal(err)
	}

	targetBlockCount := int32(b.N)
	if int32(tipHeight) < targetBlockCount {
		th.generate(uint32(targetBlockCount - int32(tipHeight)))
	}

	ctxt, cancel := context.WithTimeout(context.Background(), defaultTimeout*5)
	defer cancel()

	b.ReportAllocs()
	b.ResetTimer()

	for height := int32(0); height < targetBlockCount; height++ {
		_, _, _, err := d.GetCFilter(ctxt, height)
		if err != nil {
			th.Fatalf("unable to get cfilter: %v", err)
		}
	}
}
