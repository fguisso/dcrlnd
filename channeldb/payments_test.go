package channeldb

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/decred/dcrd/dcrec/secp256k1/v2"
	"github.com/decred/dcrlnd/lntypes"
	"github.com/decred/dcrlnd/record"
	"github.com/decred/dcrlnd/routing/route"
)

var (
	priv, _ = secp256k1.GeneratePrivateKey()
	pub     = priv.PubKey()

	testHop1 = &route.Hop{
		PubKeyBytes:      route.NewVertex(pub),
		ChannelID:        12345,
		OutgoingTimeLock: 111,
		AmtToForward:     555,
		CustomRecords: record.CustomSet{
			65536: []byte{},
			80001: []byte{},
		},
		MPP: record.NewMPP(32, [32]byte{0x42}),
	}

	testHop2 = &route.Hop{
		PubKeyBytes:      route.NewVertex(pub),
		ChannelID:        12345,
		OutgoingTimeLock: 111,
		AmtToForward:     555,
		LegacyPayload:    true,
	}

	testRoute = route.Route{
		TotalTimeLock: 123,
		TotalAmount:   1234567,
		SourcePubKey:  route.NewVertex(pub),
		Hops: []*route.Hop{
			testHop2,
			testHop1,
		},
	}
)

func makeFakeInfo() (*PaymentCreationInfo, *PaymentAttemptInfo) {
	var preimg lntypes.Preimage
	copy(preimg[:], rev[:])

	c := &PaymentCreationInfo{
		PaymentHash: preimg.Hash(),
		Value:       1000,
		// Use single second precision to avoid false positive test
		// failures due to the monotonic time component.
		CreationDate:   time.Unix(time.Now().Unix(), 0),
		PaymentRequest: []byte(""),
	}

	a := &PaymentAttemptInfo{
		PaymentID:  44,
		SessionKey: priv,
		Route:      testRoute,
	}
	return c, a
}

func TestSentPaymentSerialization(t *testing.T) {
	t.Parallel()

	c, s := makeFakeInfo()

	var b bytes.Buffer
	if err := serializePaymentCreationInfo(&b, c); err != nil {
		t.Fatalf("unable to serialize creation info: %v", err)
	}

	newCreationInfo, err := deserializePaymentCreationInfo(&b)
	if err != nil {
		t.Fatalf("unable to deserialize creation info: %v", err)
	}

	if !reflect.DeepEqual(c, newCreationInfo) {
		t.Fatalf("Payments do not match after "+
			"serialization/deserialization %v vs %v",
			spew.Sdump(c), spew.Sdump(newCreationInfo),
		)
	}

	b.Reset()
	if err := serializePaymentAttemptInfo(&b, s); err != nil {
		t.Fatalf("unable to serialize info: %v", err)
	}

	newAttemptInfo, err := deserializePaymentAttemptInfo(&b)
	if err != nil {
		t.Fatalf("unable to deserialize info: %v", err)
	}

	// First we verify all the records match up porperly, as they aren't
	// able to be properly compared using reflect.DeepEqual.
	err = assertRouteEqual(&s.Route, &newAttemptInfo.Route)
	if err != nil {
		t.Fatalf("Routes do not match after "+
			"serialization/deserialization: %v", err)
	}

	// Clear routes to allow DeepEqual to compare the remaining fields.
	newAttemptInfo.Route = route.Route{}
	s.Route = route.Route{}

	if !reflect.DeepEqual(s, newAttemptInfo) {
		s.SessionKey.Curve = nil
		newAttemptInfo.SessionKey.Curve = nil
		t.Fatalf("Payments do not match after "+
			"serialization/deserialization %v vs %v",
			spew.Sdump(s), spew.Sdump(newAttemptInfo),
		)
	}
}

// assertRouteEquals compares to routes for equality and returns an error if
// they are not equal.
func assertRouteEqual(a, b *route.Route) error {
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("PaymentAttemptInfos don't match: %v vs %v",
			spew.Sdump(a), spew.Sdump(b))
	}

	return nil
}

func TestRouteSerialization(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := SerializeRoute(&b, testRoute); err != nil {
		t.Fatal(err)
	}

	r := bytes.NewReader(b.Bytes())
	route2, err := DeserializeRoute(r)
	if err != nil {
		t.Fatal(err)
	}

	// First we verify all the records match up porperly, as they aren't
	// able to be properly compared using reflect.DeepEqual.
	err = assertRouteEqual(&testRoute, &route2)
	if err != nil {
		t.Fatalf("routes not equal: \n%v vs \n%v",
			spew.Sdump(testRoute), spew.Sdump(route2))
	}
}
