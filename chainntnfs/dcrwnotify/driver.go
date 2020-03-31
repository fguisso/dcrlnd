package dcrwnotify

import (
	"errors"
	"fmt"

	"decred.org/dcrwallet/wallet"
	"github.com/decred/dcrd/chaincfg/v2"
	"github.com/decred/dcrlnd/chainntnfs"
)

const (
	// notifierType uniquely identifies this concrete implementation of the
	// ChainNotifier interface.
	notifierType = "dcrw"
)

// createNewNotifier creates a new instance of the ChainNotifier interface
// implemented by DcrdNotifier.
func createNewNotifier(args ...interface{}) (chainntnfs.ChainNotifier, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("incorrect number of arguments to "+
			".New(...), expected 4, instead passed %v", len(args))
	}

	w, ok := args[0].(*wallet.Wallet)
	if !ok {
		return nil, errors.New("first argument to dcrdnotifier.New " +
			"is incorrect, expected a *rpcclient.ConnConfig")
	}

	chainParams, ok := args[1].(*chaincfg.Params)
	if !ok {
		return nil, errors.New("second argument to dcrdnotifier.New " +
			"is incorrect, expected a *chaincfg.Params")
	}

	spendHintCache, ok := args[2].(chainntnfs.SpendHintCache)
	if !ok {
		return nil, errors.New("third argument to dcrdnotifier.New " +
			"is incorrect, expected a chainntnfs.SpendHintCache")
	}

	confirmHintCache, ok := args[3].(chainntnfs.ConfirmHintCache)
	if !ok {
		return nil, errors.New("third argument to dcrdnotifier.New " +
			"is incorrect, expected a chainntnfs.ConfirmHintCache")
	}

	return New(w, chainParams, spendHintCache, confirmHintCache)
}

// init registers a driver for the DcrdNotifier concrete implementation of the
// chainntnfs.ChainNotifier interface.
func init() {
	// Register the driver.
	notifier := &chainntnfs.NotifierDriver{
		NotifierType: notifierType,
		New:          createNewNotifier,
	}

	if err := chainntnfs.RegisterNotifier(notifier); err != nil {
		panic(fmt.Sprintf("failed to register notifier driver '%s': %v",
			notifierType, err))
	}
}
