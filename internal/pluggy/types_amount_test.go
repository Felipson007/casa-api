package pluggy

import "testing"

func TestAccountAmount(t *testing.T) {
	cases := []struct {
		name string
		tx   Transaction
		want string
	}{
		{"brl charge", Transaction{Amount: "116.62", CurrencyCode: "BRL"}, "116.62"},
		{"usd charge uses the converted value", Transaction{Amount: "16.98", CurrencyCode: "USD", AmountInAccountCurrency: "91.86"}, "91.86"},
		{"usd refund keeps its sign", Transaction{Amount: "-16.98", CurrencyCode: "USD", AmountInAccountCurrency: "91.86"}, "-91.86"},
		{"usd without conversion falls back", Transaction{Amount: "16.98", CurrencyCode: "USD"}, "16.98"},
	}
	for _, c := range cases {
		if got := c.tx.AccountAmount("BRL").Money(); got != c.want {
			t.Errorf("%s: AccountAmount = %s, want %s", c.name, got, c.want)
		}
	}
}
