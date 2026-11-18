// Command caldav-probe checks an iCloud Calendar (CalDAV) connection against the REAL server and
// prints only STRUCTURE: which endpoint discovery selected, how many calendars exist, how many
// events the window holds and whether a create/delete round-trips. It never prints a calendar
// name, an event title or an id — the output is safe to paste into a chat or an issue.
//
// Credentials come either from the environment (local use):
//
//	ICLOUD_APPLE_ID=... ICLOUD_APP_PASSWORD=... go run ./cmd/caldav-probe
//
// or from the database's encrypted connection (cluster use, needs DATABASE_URL and
// APP_ENCRYPTION_KEY — both already in atlas-api-secret):
//
//	go run ./cmd/caldav-probe -from-db
//
// Optional: ICLOUD_CALENDAR_PATH (defaults to the first calendar found), PROBE_DAYS.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/icloudcal"
	"github.com/atlas/atlas-go/internal/integrations"
	integrationspostgres "github.com/atlas/atlas-go/internal/integrations/postgres"
)

func main() {
	fromDB := flag.Bool("from-db", false, "read the iCloud credentials from the database (needs DATABASE_URL + APP_ENCRYPTION_KEY)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	appleID := strings.TrimSpace(os.Getenv("ICLOUD_APPLE_ID"))
	password := strings.TrimSpace(os.Getenv("ICLOUD_APP_PASSWORD"))
	calendarPath := strings.TrimSpace(os.Getenv("ICLOUD_CALENDAR_PATH"))

	if *fromDB {
		dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dsn == "" {
			fmt.Println("DATABASE_URL is required with -from-db")
			os.Exit(2)
		}
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			fmt.Println("db connect: FAILED —", err)
			os.Exit(1)
		}
		defer pool.Close()
		svc := integrations.NewService(integrationspostgres.NewRepository(pool), nil)
		creds, ok, err := svc.ICloudCredentials(ctx)
		if err != nil {
			fmt.Println("read credentials: FAILED —", err)
			os.Exit(1)
		}
		if !ok {
			fmt.Println("no ICLOUD_CALENDAR connection stored")
			os.Exit(1)
		}
		appleID, password = creds.AppleID, creds.AppPassword
		if calendarPath == "" {
			calendarPath = creds.CalendarPath
		}
		fmt.Println("credentials: loaded from db; selected calendar present:", calendarPath != "")
	}

	if appleID == "" || password == "" {
		fmt.Println("set ICLOUD_APPLE_ID and ICLOUD_APP_PASSWORD (or use -from-db)")
		os.Exit(2)
	}

	client, err := icloudcal.NewCaldavClient(ctx, appleID, password)
	if err != nil {
		fmt.Println("client: FAILED —", err)
		os.Exit(1)
	}
	fmt.Println("discovered endpoint:", client.Endpoint())

	cals, err := client.ListCalendars(ctx)
	if err != nil {
		fmt.Println("list calendars: FAILED —", err)
		os.Exit(1)
	}
	fmt.Printf("calendars found: %d\n", len(cals))
	for i, c := range cals {
		fmt.Printf("  [%d] path=%s name_len=%d\n", i, c.Path, len(c.Name))
	}
	if len(cals) == 0 {
		os.Exit(1)
	}
	if calendarPath == "" {
		calendarPath = cals[0].Path
	}
	fmt.Println("using stored calendar path:", calendarPath)

	days := 30
	if v, err := strconv.Atoi(os.Getenv("PROBE_DAYS")); err == nil && v > 0 {
		days = v
	}
	now := time.Now()
	events, err := client.ListEvents(ctx, calendarPath, now.AddDate(0, 0, -days), now.AddDate(0, 0, days))
	if err != nil {
		fmt.Println("list events: FAILED —", err)
		os.Exit(1)
	}
	fmt.Printf("events in +/-%d days: %d\n", days, len(events))

	uid := fmt.Sprintf("atlas-probe-%d", now.UnixNano())
	stored, err := client.PutEvent(ctx, calendarPath+uid+".ics", uid, icloudcal.EventData{
		Title:     "Atlas probe",
		AllDay:    true,
		StartDate: now.AddDate(0, 0, 1),
	})
	if err != nil {
		fmt.Println("create: FAILED —", err)
		os.Exit(1)
	}
	fmt.Println("create: OK (etag present:", stored.ETag != "", ")")
	if err := client.DeleteEvent(ctx, stored.Href); err != nil {
		fmt.Println("delete: FAILED —", err)
		os.Exit(1)
	}
	fmt.Println("delete: OK")
}
