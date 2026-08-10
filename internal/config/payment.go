package config

// ─────────────────────────────────────────────────────────────────────────────
// PAYMENT PROVIDER ABSTRACTION
//
// To switch from Mock → Paystack:
//   1. Set PAYMENT_PROVIDER=paystack in your .env
//   2. Set PAYSTACK_SECRET_KEY=sk_live_...  in your .env
//   3. The PaystackProvider below is a thin stub — implement the actual
//      HTTP calls to Paystack's /transaction/initialize and /transaction/verify
//      endpoints. See: https://paystack.com/docs/api/transaction/
//
// For Flutterwave swap out for FlutterwaveProvider and implement
//   POST /v3/payments/initialize + GET /v3/transactions/:id/verify
// ─────────────────────────────────────────────────────────────────────────────

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// PaymentProvider is the interface all payment drivers must satisfy.
type PaymentProvider interface {
	// InitializePayment starts a payment session.
	// Returns an authorization URL (or reference for in-app wallets) and a
	// unique reference string that can be used to verify the payment later.
	InitializePayment(req InitPaymentRequest) (*InitPaymentResponse, error)

	// VerifyPayment checks whether a payment went through.
	VerifyPayment(reference string) (*VerifyPaymentResponse, error)

	// ProviderName returns a short label used for logging ("mock", "paystack").
	ProviderName() string
}

// ── REQUEST / RESPONSE DTOs ───────────────────────────────────────────────────

type InitPaymentRequest struct {
	AmountKobo  int64  // amount in smallest currency unit (kobo for NGN)
	Email       string
	Reference   string
	CallbackURL string
	Metadata    map[string]string
}

type InitPaymentResponse struct {
	AuthorizationURL string // redirect user here (empty for in-app wallet flow)
	Reference        string
	AccessCode       string
}

type VerifyPaymentResponse struct {
	Paid      bool
	Reference string
	AmountKobo int64
	Message   string
}

// ── PROVIDER FACTORY ─────────────────────────────────────────────────────────

// NewPaymentProvider reads PAYMENT_PROVIDER from the environment and returns
// the matching driver.  Defaults to "mock" if not set.
func NewPaymentProvider() PaymentProvider {
	switch os.Getenv("PAYMENT_PROVIDER") {
	case "paystack":
		return &PaystackProvider{SecretKey: os.Getenv("PAYSTACK_SECRET_KEY")}
	case "flutterwave":
		return &FlutterwaveProvider{SecretKey: os.Getenv("FLW_SECRET_KEY")}
	default:
		return &MockProvider{}
	}
}

// ── MOCK PROVIDER (for testing) ───────────────────────────────────────────────
// Every payment is immediately "successful".  Use this to test the full
// checkout → escrow → delivery → release flow without real money.

type MockProvider struct{}

func (m *MockProvider) ProviderName() string { return "mock" }

func (m *MockProvider) InitializePayment(req InitPaymentRequest) (*InitPaymentResponse, error) {
	ref := req.Reference
	if ref == "" {
		ref = generateReference()
	}
	// In a real provider you'd hit an API; here we just echo back.
	return &InitPaymentResponse{
		AuthorizationURL: fmt.Sprintf("mock://pay/%s", ref),
		Reference:        ref,
		AccessCode:       "mock_access_" + ref,
	}, nil
}

func (m *MockProvider) VerifyPayment(reference string) (*VerifyPaymentResponse, error) {
	// Mock always approves — swap this logic to simulate failures in tests.
	return &VerifyPaymentResponse{
		Paid:       true,
		Reference:  reference,
		AmountKobo: 0, // mock doesn't track amount; service layer asserts
		Message:    "mock payment verified",
	}, nil
}

// ── PAYSTACK PROVIDER (stub — fill in HTTP calls when ready) ─────────────────
// Uncomment and implement when you're ready to go live.

type PaystackProvider struct {
	SecretKey string
}

func (p *PaystackProvider) ProviderName() string { return "paystack" }

func (p *PaystackProvider) InitializePayment(req InitPaymentRequest) (*InitPaymentResponse, error) {
	// TODO: POST https://api.paystack.co/transaction/initialize
	// Headers: Authorization: Bearer <SecretKey>
	// Body:    { email, amount (kobo), reference, callback_url, metadata }
	// Response example:
	//   { "status": true, "data": { "authorization_url": "...", "access_code": "...", "reference": "..." } }

	_ = req // remove when implemented
	return nil, fmt.Errorf("paystack provider not yet implemented — set PAYMENT_PROVIDER=mock for testing")
}

func (p *PaystackProvider) VerifyPayment(reference string) (*VerifyPaymentResponse, error) {
	// TODO: GET https://api.paystack.co/transaction/verify/:reference
	// Headers: Authorization: Bearer <SecretKey>
	// Response: { "status": true, "data": { "status": "success", "amount": ... } }

	_ = reference
	return nil, fmt.Errorf("paystack provider not yet implemented — set PAYMENT_PROVIDER=mock for testing")
}

// ── FLUTTERWAVE PROVIDER (stub) ───────────────────────────────────────────────

type FlutterwaveProvider struct {
	SecretKey string
}

func (f *FlutterwaveProvider) ProviderName() string { return "flutterwave" }

func (f *FlutterwaveProvider) InitializePayment(req InitPaymentRequest) (*InitPaymentResponse, error) {
	// TODO: POST https://api.flutterwave.com/v3/payments
	_ = req
	return nil, fmt.Errorf("flutterwave provider not yet implemented — set PAYMENT_PROVIDER=mock for testing")
}

func (f *FlutterwaveProvider) VerifyPayment(reference string) (*VerifyPaymentResponse, error) {
	// TODO: GET https://api.flutterwave.com/v3/transactions/:id/verify
	_ = reference
	return nil, fmt.Errorf("flutterwave provider not yet implemented — set PAYMENT_PROVIDER=mock for testing")
}

// ── HELPERS ───────────────────────────────────────────────────────────────────

func generateReference() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return fmt.Sprintf("MKT_%s_%d", hex.EncodeToString(b), time.Now().UnixMilli())
}
