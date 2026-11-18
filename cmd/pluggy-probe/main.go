// Command pluggy-probe checks a Pluggy connection against the REAL API and prints only STRUCTURE:
// counts, field presence and how Atlas would classify what it sees. It never prints an amount, a
// description, an account number, a name or an id — the output is safe to paste into a chat or an
// issue, which is the point: it answers "does my bank expose faturas? investments? installments?
// how many transactions would be counted vs ignored?" without leaking anything.
//
// It reads the same variables the worker's environment fallback does and touches nothing in the
// database:
//
//	PLUGGY_CLIENT_ID=... PLUGGY_CLIENT_SECRET=... PLUGGY_ITEM_IDS=id1,id2 go run ./cmd/pluggy-probe
//
// Optional: PROBE_DAYS (history window, default 90).
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atlas/atlas-go/internal/openfinance"
	"github.com/atlas/atlas-go/internal/pluggy"
)

func main() {
	id, secret := strings.TrimSpace(os.Getenv("PLUGGY_CLIENT_ID")), strings.TrimSpace(os.Getenv("PLUGGY_CLIENT_SECRET"))
	if id == "" || secret == "" {
		fmt.Println("set PLUGGY_CLIENT_ID and PLUGGY_CLIENT_SECRET (and PLUGGY_ITEM_IDS)")
		os.Exit(2)
	}
	client, err := pluggy.New(pluggy.Config{ClientID: id, ClientSecret: secret})
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(2)
	}
	days := 90
	if v, err := strconv.Atoi(os.Getenv("PROBE_DAYS")); err == nil && v > 0 {
		days = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	items := strings.FieldsFunc(os.Getenv("PLUGGY_ITEM_IDS"), func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' })
	if len(items) == 0 {
		// No item ids: at least prove the credentials work (a bad secret fails at /auth).
		if _, err := client.Item(ctx, "00000000-0000-0000-0000-000000000000"); err != nil && !pluggy.IsNotFound(err) {
			fmt.Println("credentials: FAILED —", err)
			os.Exit(1)
		}
		fmt.Println("credentials: OK (authenticated). Set PLUGGY_ITEM_IDS to probe your connections.")
		return
	}

	to := time.Now().Format("2006-01-02")
	from := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	fmt.Printf("window: last %d days\n", days)
	for n, itemID := range items {
		fmt.Printf("\n== connection #%d ==\n", n+1)
		item, err := client.Item(ctx, itemID)
		if err != nil {
			fmt.Println("item: FAILED —", err)
			continue
		}
		inst := ""
		if item.Connector != nil {
			inst = item.Connector.Name
		}
		fmt.Printf("institution: %s\nstatus: %s / %s   usable-for-sync: %v   needs-attention: %v\n",
			inst, item.Status, item.ExecutionStatus, openfinance.SyncUsable(item.Status, item.ExecutionStatus), openfinance.NeedsAttention(item.Status, item.ExecutionStatus))

		accounts, err := client.Accounts(ctx, itemID)
		if err != nil {
			fmt.Println("accounts: FAILED —", err)
			continue
		}
		fmt.Printf("accounts: %d\n", len(accounts))
		kinds := map[openfinance.ProjectionKind]int{}
		cats := map[string]int{}
		for i, a := range accounts {
			fmt.Printf("  - account %d: type=%s subtype=%s balance=%v", i+1, a.Type, a.Subtype, a.Balance.IsSet())
			if a.CreditData != nil {
				fmt.Printf(" creditLimit=%v availableLimit=%v", a.CreditData.CreditLimit.IsSet(), a.CreditData.AvailableCreditLimit.IsSet())
			}
			fmt.Println()
			txs, err := client.Transactions(ctx, a.ID, from, to)
			if err != nil {
				fmt.Println("    transactions: FAILED —", err)
				continue
			}
			var withCard, withBill, withInst, pending int
			for _, t := range txs {
				if t.Status == "PENDING" {
					pending++
				}
				if t.CreditCardMetadata != nil {
					withCard++
					if t.CreditCardMetadata.BillID != "" {
						withBill++
					}
					if t.CreditCardMetadata.TotalInstallments != nil {
						withInst++
					}
				}
				if t.Category != "" {
					cats[t.Category]++
				}
				kinds[openfinance.Classify(openfinance.ClassifyInput{
					AccountType: a.Type, Direction: t.Type, Amount: t.Amount, Status: t.Status,
					Description: t.Description, ProviderCategory: t.Category,
				})]++
			}
			fmt.Printf("    transactions: %d (pending %d)  creditCardMetadata: %d  with billId: %d  with installments: %d\n",
				len(txs), pending, withCard, withBill, withInst)
			if a.Type == openfinance.AccountTypeCredit {
				bills, err := client.Bills(ctx, a.ID)
				switch {
				case err != nil:
					fmt.Println("    bills: FAILED —", err)
				default:
					withDue, withTotal := 0, 0
					for _, b := range bills {
						if b.DueDate != "" {
							withDue++
						}
						if b.TotalAmount.IsSet() {
							withTotal++
						}
					}
					fmt.Printf("    bills: %d (with dueDate %d, with totalAmount %d)\n", len(bills), withDue, withTotal)
				}
			}
		}
		fmt.Println("how Atlas would classify these transactions:")
		order := []openfinance.ProjectionKind{openfinance.KindExpense, openfinance.KindIncome, openfinance.KindIgnoredCardPayment, openfinance.KindIgnoredTransfer, openfinance.KindIgnoredInvestment, openfinance.KindPending}
		for _, k := range order {
			fmt.Printf("  %-22s %d\n", k, kinds[k])
		}
		if len(cats) > 0 {
			names := make([]string, 0, len(cats))
			for c := range cats {
				names = append(names, c)
			}
			sort.Strings(names)
			fmt.Println("provider categories seen (names only):", strings.Join(names, " | "))
		}

		inv, err := client.Investments(ctx, itemID)
		switch {
		case err != nil:
			fmt.Println("investments: FAILED —", err)
		default:
			byType := map[string]int{}
			for _, v := range inv {
				byType[v.Type]++
			}
			fmt.Printf("investments: %d %v\n", len(inv), byType)
		}
	}
}
