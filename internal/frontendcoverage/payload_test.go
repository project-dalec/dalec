package frontendcoverage

import (
	"bytes"
	"errors"
	"testing"
)

func TestPayloadFromErrorHandlesJoinedGRPCErrors(t *testing.T) {
	payload := &Payload{
		MetaGz:     []byte("meta-gz"),
		CountersGz: []byte("counters-gz"),
	}

	errWithPayload, err := payload.AttachToError(errors.New("solve failed"))
	if err != nil {
		t.Fatalf("expected nil attach error, got %v", err)
	}

	joined := errors.Join(errors.New("secondary failure"), errWithPayload)
	got, err := PayloadFromError(joined)
	if err != nil {
		t.Fatalf("expected nil payload error, got %v", err)
	}
	if got == nil {
		t.Fatal("expected payload to be extracted from joined error")
	}
	if !bytes.Equal(got.MetaGz, payload.MetaGz) {
		t.Fatalf("unexpected meta payload: %q", got.MetaGz)
	}
	if !bytes.Equal(got.CountersGz, payload.CountersGz) {
		t.Fatalf("unexpected counters payload: %q", got.CountersGz)
	}
}
