package attribution

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func testBuilderCode() [20]byte {
	var code [20]byte
	copy(code[:], "test-builder-code-00")
	return code
}

func testEncoder(t *testing.T) AttributionEncoder {
	t.Helper()
	enc, err := NewEncoder(Config{
		BuilderCode: testBuilderCode(),
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	return enc
}

func TestEncode_AppendsAttribution(t *testing.T) {
	enc := testEncoder(t)
	ctx := context.Background()
	calldata := []byte{0x01, 0x02, 0x03, 0x04}

	encoded, err := enc.Encode(ctx, calldata)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be original + 4 magic + 20 builder code = 4 + 24 = 28 bytes.
	expected := len(calldata) + len(AttributionMagic) + BuilderCodeLength
	if len(encoded) != expected {
		t.Errorf("expected encoded length %d, got %d", expected, len(encoded))
	}

	// Original calldata should be at the start.
	if !bytes.Equal(encoded[:len(calldata)], calldata) {
		t.Error("original calldata should be preserved at start of encoded data")
	}
}

func TestEncode_DisabledReturnsOriginal(t *testing.T) {
	enc, err := NewEncoder(Config{
		BuilderCode: testBuilderCode(),
		Enabled:     false,
	})
	if err != nil {
		t.Fatalf("failed to create disabled encoder: %v", err)
	}

	ctx := context.Background()
	calldata := []byte{0x01, 0x02, 0x03, 0x04}

	encoded, err := enc.Encode(ctx, calldata)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(encoded, calldata) {
		t.Error("disabled encoder should return original calldata unchanged")
	}
}

func TestEncode_DoesNotMutateInput(t *testing.T) {
	enc := testEncoder(t)
	ctx := context.Background()
	original := []byte{0xde, 0xad, 0xbe, 0xef}
	inputCopy := make([]byte, len(original))
	copy(inputCopy, original)

	_, err := enc.Encode(ctx, original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(original, inputCopy) {
		t.Error("Encode should not mutate the input byte slice")
	}
}

func TestEncode_EmptyCalldata(t *testing.T) {
	enc := testEncoder(t)
	ctx := context.Background()

	encoded, err := enc.Encode(ctx, []byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be just the magic + builder code (24 bytes).
	expected := len(AttributionMagic) + BuilderCodeLength
	if len(encoded) != expected {
		t.Errorf("expected %d bytes, got %d", expected, len(encoded))
	}
}

func TestEncode_LargeCalldata(t *testing.T) {
	enc := testEncoder(t)
	ctx := context.Background()
	calldata := make([]byte, 4096) // large calldata
	for i := range calldata {
		calldata[i] = byte(i % 256)
	}

	encoded, err := enc.Encode(ctx, calldata)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := len(calldata) + len(AttributionMagic) + BuilderCodeLength
	if len(encoded) != expected {
		t.Errorf("expected %d bytes, got %d", expected, len(encoded))
	}

	// Verify original calldata is preserved.
	if !bytes.Equal(encoded[:len(calldata)], calldata) {
		t.Error("large calldata not preserved correctly")
	}
}

func TestDecode_WithAttribution(t *testing.T) {
	enc := testEncoder(t)
	ctx := context.Background()
	original := []byte{0xde, 0xad, 0xbe, 0xef}

	encoded, err := enc.Encode(ctx, original)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	attr, err := enc.Decode(ctx, encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if attr == nil {
		t.Fatal("expected attribution, got nil")
	}

	expected := testBuilderCode()
	if attr.BuilderCode != expected {
		t.Errorf("expected builder code %v, got %v", expected, attr.BuilderCode)
	}
	if attr.Version != 1 {
		t.Errorf("expected version 1, got %d", attr.Version)
	}
}

func TestRoundtrip(t *testing.T) {
	tests := []struct {
		name     string
		calldata []byte
	}{
		{name: "empty calldata", calldata: []byte{}},
		{name: "small calldata", calldata: []byte{0x01, 0x02}},
		{name: "function selector", calldata: []byte{0xa9, 0x05, 0x9c, 0xbb, 0x00, 0x00, 0x00, 0x01}},
		{name: "large calldata", calldata: make([]byte, 256)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := testEncoder(t)
			ctx := context.Background()

			encoded, err := enc.Encode(ctx, tt.calldata)
			if err != nil {
				t.Fatalf("encode error: %v", err)
			}

			attr, err := enc.Decode(ctx, encoded)
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}

			expected := testBuilderCode()
			if attr.BuilderCode != expected {
				t.Errorf("roundtrip builder code mismatch: expected %v, got %v", expected, attr.BuilderCode)
			}

			// Verify the original calldata is preserved.
			originalPart := encoded[:len(encoded)-len(AttributionMagic)-BuilderCodeLength]
			if !bytes.Equal(originalPart, tt.calldata) {
				t.Error("original calldata not preserved after roundtrip")
			}
		})
	}
}

func TestDecode_WithoutAttribution(t *testing.T) {
	enc := testEncoder(t)
	ctx := context.Background()

	// Calldata without attribution magic.
	calldata := make([]byte, 32)
	for i := range calldata {
		calldata[i] = 0xff
	}

	_, err := enc.Decode(ctx, calldata)
	if err == nil {
		t.Fatal("expected error for calldata without attribution")
	}
	if !errors.Is(err, ErrNoAttribution) {
		t.Errorf("expected ErrNoAttribution, got %v", err)
	}
}

func TestDecode_TooShort(t *testing.T) {
	enc := testEncoder(t)
	ctx := context.Background()

	calldata := []byte{0x01, 0x02, 0x03}

	_, err := enc.Decode(ctx, calldata)
	if err == nil {
		t.Fatal("expected error for too-short calldata")
	}
	if !errors.Is(err, ErrInvalidCalldata) {
		t.Errorf("expected ErrInvalidCalldata, got %v", err)
	}
}

func TestNewEncoder_EmptyCode(t *testing.T) {
	_, err := NewEncoder(Config{
		Enabled: true,
		// BuilderCode is zero value (all zeros)
	})
	if err == nil {
		t.Fatal("expected error for empty builder code when enabled")
	}
}

func TestNewEncoder_DisabledEmptyCode(t *testing.T) {
	enc, err := NewEncoder(Config{
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("disabled encoder should accept empty code: %v", err)
	}
	if enc == nil {
		t.Fatal("expected encoder, got nil")
	}
}

func TestBuilderCodeHex(t *testing.T) {
	var code [20]byte
	for i := range code {
		code[i] = byte(i)
	}

	attr := Attribution{BuilderCode: code}
	hex := attr.BuilderCodeHex()

	if len(hex) != 42 { // "0x" + 40 hex chars
		t.Errorf("expected 42 char hex string, got %d: %s", len(hex), hex)
	}
	if hex[:2] != "0x" {
		t.Errorf("expected 0x prefix, got %s", hex[:2])
	}
}

func TestEncode_ContextCancelled(t *testing.T) {
	enc := testEncoder(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := enc.Encode(ctx, []byte{0x01})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
