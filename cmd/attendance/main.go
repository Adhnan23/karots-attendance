// Command attendance is the shop attendance server: one static binary + one SQLite file.
//
//	attendance [-addr :8080] [-db attendance.db] [-proxy]          run the server
//	attendance [-db attendance.db] admin <phone> <password> [name]  create or reset an admin
//	attendance [-db attendance.db] backup <file.db>                 consistent copy while running
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // timezone data inside the binary; run with TZ=Asia/Colombo

	"github.com/Adhnan23/karots-attendance/internal/auth"
	"github.com/Adhnan23/karots-attendance/internal/db"
	"github.com/Adhnan23/karots-attendance/internal/web"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dbPath := flag.String("db", "attendance.db", "SQLite database file")
	proxy := flag.Bool("proxy", false, "running behind Caddy/nginx: trust X-Forwarded-For and X-Forwarded-Proto")
	flag.Parse()

	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	if args := flag.Args(); len(args) > 0 {
		if err := command(conn, args); err != nil {
			log.Fatal(err)
		}
		return
	}

	srv := &http.Server{Addr: *addr, Handler: web.New(conn, *proxy).Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: time.Minute, IdleTimeout: 2 * time.Minute}
	go func() {
		log.Printf("listening on %s (timezone %s)", *addr, time.Now().Location())
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx) // finish in-flight requests so SQLite closes cleanly
}

const usage = `usage:
  attendance [-db file] admin <phone> <password> [name]   create or reset an admin
  attendance [-db file] backup <file.db>                   consistent copy, safe while the server runs`

func command(conn *sql.DB, args []string) error {
	if args[0] == "backup" && len(args) == 2 {
		if _, err := os.Stat(args[1]); err == nil {
			return errors.New(args[1] + " already exists")
		}
		if _, err := conn.Exec(`VACUUM INTO ?`, args[1]); err != nil {
			return err
		}
		fmt.Println("backup written:", args[1])
		return nil
	}
	if args[0] != "admin" || len(args) < 3 {
		return errors.New(usage)
	}
	phone, pw, name := args[1], args[2], "Admin"
	if len(args) > 3 {
		name = args[3]
	}
	if len(pw) < 8 {
		return errors.New("admin password must be at least 8 characters")
	}
	var id int64
	err := conn.QueryRow(`INSERT INTO users(name,phone,pw,role) VALUES(?,?,?,'admin')
		ON CONFLICT(phone) DO UPDATE SET pw=excluded.pw, role='admin', active=1 RETURNING id`,
		name, phone, auth.HashPassword(pw)).Scan(&id)
	if err != nil {
		return err
	}
	if _, err := conn.Exec(`DELETE FROM logins WHERE user_id=?`, id); err != nil { // old sessions die with the old password
		return err
	}
	fmt.Println("admin saved:", phone)
	return nil
}
