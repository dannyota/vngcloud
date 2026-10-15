package iam

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"danny.vn/vngcloud"
)

func TestPasswordBoundsAndClasses(t *testing.T) {
	for _, n := range []int{0, 8, 32, 50} {
		// Synthetic entropy repeats a complete alphabet, including all classes.
		p, err := generatePassword(context.Background(), n, bytes.NewReader(bytes.Repeat([]byte{0, 26, 52, 62}, 2000)))
		want := n
		if want == 0 {
			want = 32
		}
		if err != nil || len(p) != want || !passwordClasses(p) {
			t.Fatalf("length %d: length=%d err=%v", n, len(p), err)
		}
	}
	for _, n := range []int{-1, 7, 51} {
		if _, err := generatePassword(context.Background(), n, bytes.NewReader(nil)); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("length %d: %v", n, err)
		}
	}
}

func TestPasswordRejectionDistribution(t *testing.T) {
	// Each accepted byte has exactly four preimages; 252..255 are discarded.
	counts := make(map[byte]int)
	for draw := 0; draw < 252; draw++ {
		raw := []byte{byte(draw), 0, 26, 52, 62, 0, 26, 52}
		p, err := generatePassword(context.Background(), 8, bytes.NewReader(raw))
		if err != nil || p[0] != passwordAlphabet[draw%63] {
			t.Fatal("draw mapping failed")
		}
		counts[p[0]]++
	}
	for _, b := range []byte(passwordAlphabet) {
		if counts[b] != 4 {
			t.Fatal("biased alphabet")
		}
	}
	// Discarded values must not take a position in the candidate.
	raw := append([]byte{252, 253, 254, 255}, []byte{0, 26, 52, 62, 0, 26, 52, 62}...)
	p, err := generatePassword(context.Background(), 8, bytes.NewReader(raw))
	if err != nil || !bytes.Equal(p, []byte{passwordAlphabet[0], passwordAlphabet[26], passwordAlphabet[52], passwordAlphabet[62], passwordAlphabet[0], passwordAlphabet[26], passwordAlphabet[52], passwordAlphabet[62]}) {
		t.Fatal("rejection sampling changed positions")
	}
	// Missing-class candidates are rejected as a whole, never repaired.
	raw = append(bytes.Repeat([]byte{0}, 8), raw...)
	p, err = generatePassword(context.Background(), 8, bytes.NewReader(raw))
	if err != nil || !bytes.Equal(p, []byte{passwordAlphabet[0], passwordAlphabet[26], passwordAlphabet[52], passwordAlphabet[62], passwordAlphabet[0], passwordAlphabet[26], passwordAlphabet[52], passwordAlphabet[62]}) {
		t.Fatal("candidate rejection failed")
	}
}

type passwordEntropyFailure struct{}

func (passwordEntropyFailure) Read([]byte) (int, error) { return 0, errors.New("entropy-input-canary") }

func TestPasswordFailureAndBudgets(t *testing.T) {
	for _, reader := range []interface{ Read([]byte) (int, error) }{passwordEntropyFailure{}, bytes.NewReader(bytes.Repeat([]byte{0}, 256*8)), bytes.NewReader(bytes.Repeat([]byte{255}, 64<<10))} {
		if p, err := generatePassword(context.Background(), 8, reader); err == nil || p != nil || bytes.Contains([]byte(err.Error()), []byte("canary")) {
			t.Fatal("unsafe entropy failure")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := generatePassword(ctx, 8, passwordEntropyFailure{}); p != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
}

func TestPasswordFreshValues(t *testing.T) {
	a, err := createPassword(context.Background(), &CreateUserInput{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := createPassword(context.Background(), &CreateUserInput{})
	if err != nil {
		t.Fatal(err)
	}
	if a == b || len(a.Reveal()) != 32 || len(b.Reveal()) != 32 {
		t.Fatal("passwords not fresh")
	}
}
