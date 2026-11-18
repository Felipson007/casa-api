package pluggy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Decimal is a JSON number (or numeric string, or null) kept as its literal text. Pluggy money
// fields arrive as JSON numbers; decoding them straight into float64 would reintroduce exactly the
// rounding error the rest of this app avoids (money is numeric(15,2) as string everywhere).
type Decimal string

// UnmarshalJSON accepts 12.5, "12.5" and null (→ empty Decimal).
func (d *Decimal) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*d = ""
		return nil
	}
	s := strings.Trim(string(b), `"`)
	if _, ok := new(big.Rat).SetString(s); !ok {
		return fmt.Errorf("pluggy: %q is not a decimal number", s)
	}
	*d = Decimal(s)
	return nil
}

// IsSet reports whether the API actually sent a value (null/absent → false).
func (d Decimal) IsSet() bool { return d != "" }

// Rat returns the exact value, or zero when unset.
func (d Decimal) Rat() *big.Rat {
	r, ok := new(big.Rat).SetString(string(d))
	if !ok {
		return new(big.Rat)
	}
	return r
}

// Money renders the value as a fixed 2-decimal string (half away from zero), the numeric(15,2)
// shape the database and DTOs use.
func (d Decimal) Money() string { return d.Rat().FloatString(2) }

// Precise renders the value with the given number of decimals (quantities, rates).
func (d Decimal) Precise(scale int) string { return d.Rat().FloatString(scale) }

// Neg returns the negated value.
func (d Decimal) Neg() Decimal { return Decimal(new(big.Rat).Neg(d.Rat()).FloatString(8)) }

// Sign is -1, 0 or +1.
func (d Decimal) Sign() int { return d.Rat().Sign() }

// DateOf returns the calendar date (YYYY-MM-DD) of an ISO-8601 timestamp WITHOUT converting time
// zones: a purchase stamped 2026-08-31T23:30:00-03:00 belongs to 08-31 for the household, even
// though it is 09-01 in UTC. Returns "" when s is too short to hold a date.
func DateOf(s string) string {
	if len(s) < 10 {
		return ""
	}
	return s[:10]
}

// Item is one bank connection (a "Meu Pluggy" link to one institution).
type Item struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	ExecutionStatus string     `json:"executionStatus"`
	LastUpdatedAt   string     `json:"lastUpdatedAt"`
	Connector       *Connector `json:"connector"`
	Error           *ItemError `json:"error"`
}

// Connector describes the institution behind an Item.
type Connector struct {
	ID              any    `json:"id"`
	Name            string `json:"name"`
	InstitutionName string `json:"institutionName"`
}

// ItemError is the failure Pluggy attaches to an Item that could not be synced.
type ItemError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Account is a bank or credit-card account under an Item.
type Account struct {
	ID           string      `json:"id"`
	Type         string      `json:"type"` // BANK | CREDIT
	Subtype      string      `json:"subtype"`
	Name         string      `json:"name"`
	Number       string      `json:"number"`
	ItemID       string      `json:"itemId"`
	Balance      Decimal     `json:"balance"`
	CurrencyCode string      `json:"currencyCode"`
	CreditData   *CreditData `json:"creditData"`
}

// CreditData is the card-specific part of a CREDIT Account.
type CreditData struct {
	CreditLimit          Decimal `json:"creditLimit"`
	AvailableCreditLimit Decimal `json:"availableCreditLimit"`
}

// Transaction is one movement on an Account.
type Transaction struct {
	ID           string  `json:"id"`
	AccountID    string  `json:"accountId"`
	Date         string  `json:"date"`
	Description  string  `json:"description"`
	Category     string  `json:"category"`
	Amount       Decimal `json:"amount"`
	CurrencyCode string  `json:"currencyCode"`
	// AmountInAccountCurrency is Amount converted to the account's currency; Pluggy sends it for
	// foreign-currency movements, where Amount itself is in CurrencyCode (US$ 16.98, not R$ 91.86).
	AmountInAccountCurrency Decimal             `json:"amountInAccountCurrency"`
	Type                    string              `json:"type"`   // DEBIT | CREDIT
	Status                  string              `json:"status"` // PENDING | POSTED
	CreditCardMetadata      *CreditCardMetadata `json:"creditCardMetadata"`
}

// CreditCardMetadata carries installment and statement info on card transactions.
type CreditCardMetadata struct {
	InstallmentNumber *int   `json:"installmentNumber"`
	TotalInstallments *int   `json:"totalInstallments"`
	BillID            string `json:"billId"`
	// PurchaseDate is when the card was actually used. Some connectors (Itaú) stamp Transaction.Date
	// with the statement/posting date instead — every purchase of a bill landing on the same day.
	PurchaseDate string `json:"purchaseDate"`
	// CardNumber is the last digits of the physical/virtual card used (titular, adicional, virtual).
	CardNumber string `json:"cardNumber"`
}

// PurchaseDay is the calendar date the purchase happened: creditCardMetadata.purchaseDate when the
// connector sends it, otherwise Transaction.Date.
func (t Transaction) PurchaseDay() string {
	if t.CreditCardMetadata != nil {
		if d := DateOf(t.CreditCardMetadata.PurchaseDate); d != "" {
			return d
		}
	}
	return DateOf(t.Date)
}

// AccountAmount is the movement in the account's own currency: AmountInAccountCurrency for a
// foreign-currency movement (keeping Amount's sign when the converted value comes unsigned),
// Amount otherwise.
func (t Transaction) AccountAmount(accountCurrency string) Decimal {
	if accountCurrency == "" {
		accountCurrency = "BRL"
	}
	if !t.AmountInAccountCurrency.IsSet() || t.CurrencyCode == "" || strings.EqualFold(t.CurrencyCode, accountCurrency) {
		return t.Amount
	}
	converted := t.AmountInAccountCurrency
	if converted.Sign() != 0 && t.Amount.Sign() != 0 && converted.Sign() != t.Amount.Sign() {
		converted = converted.Neg()
	}
	return converted
}

// CardLast4 is the last 4 digits of the card used, or "" when unknown.
func (t Transaction) CardLast4() string {
	if t.CreditCardMetadata == nil {
		return ""
	}
	digits := make([]byte, 0, 4)
	for i := len(t.CreditCardMetadata.CardNumber) - 1; i >= 0 && len(digits) < 4; i-- {
		if c := t.CreditCardMetadata.CardNumber[i]; c >= '0' && c <= '9' {
			digits = append([]byte{c}, digits...)
		}
	}
	return string(digits)
}

// Bill is one credit-card statement (fatura).
type Bill struct {
	ID                   string          `json:"id"`
	DueDate              string          `json:"dueDate"`
	TotalAmount          Decimal         `json:"totalAmount"`
	MinimumPaymentAmount Decimal         `json:"minimumPaymentAmount"`
	AllowsInstallments   *bool           `json:"allowsInstallments"`
	FinanceCharges       []FinanceCharge `json:"financeCharges"`
}

// FinanceCharge is interest/IOF/fee billed on a Bill.
type FinanceCharge struct {
	Type   string  `json:"type"`
	Amount Decimal `json:"amount"`
}

// Investment is one position under an Item.
type Investment struct {
	ID                   string  `json:"id"`
	Code                 string  `json:"code"`
	Name                 string  `json:"name"`
	Type                 string  `json:"type"`
	Subtype              string  `json:"subtype"`
	Status               string  `json:"status"`
	CurrencyCode         string  `json:"currencyCode"`
	Balance              Decimal `json:"balance"`
	AmountOriginal       Decimal `json:"amountOriginal"`
	AmountProfit         Decimal `json:"amountProfit"`
	Quantity             Decimal `json:"quantity"`
	AnnualRate           Decimal `json:"annualRate"`
	LastMonthRate        Decimal `json:"lastMonthRate"`
	LastTwelveMonthsRate Decimal `json:"lastTwelveMonthsRate"`
	DueDate              string  `json:"dueDate"`
}

type page struct {
	Total      int               `json:"total"`
	TotalPages int               `json:"totalPages"`
	Results    []json.RawMessage `json:"results"`
}
