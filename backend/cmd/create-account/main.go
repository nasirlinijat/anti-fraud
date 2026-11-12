// Command create-account adds a console account. The password is read from
// standard input so that it never appears in the shell history or process list.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/account"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"golang.org/x/term"
)

func main() {
	database := flag.String("database", os.Getenv("DATABASE_URL"), "database (default DATABASE_URL)")
	username := flag.String("username", "", "username of the new account")
	role := flag.String("role", string(models.RoleAdmin), "reviewer or admin")
	flag.Parse()
	if *database == "" || *username == "" {
		log.Fatal("set DATABASE_URL and -username")
	}
	password, err := readPassword(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, *database)
	if err != nil {
		log.Fatal("invalid database configuration")
	}
	defer pool.Close()
	created, err := account.NewService(storage.NewAccountStore(pool)).Create(ctx, account.NewAccount{
		Username: *username, Password: password, Role: models.Role(*role),
	})
	if err != nil {
		log.Fatalf("create account: %v", err)
	}
	fmt.Printf("created %s account %s\n", created.Role, created.Username)
}

// readPassword asks twice without echo when stdin is a terminal, and otherwise
// reads the first line, so that the command also works in setup scripts.
func readPassword(stdin *os.File) (string, error) {
	fd := int(stdin.Fd())
	if !term.IsTerminal(fd) {
		return firstLine(stdin)
	}
	password, err := promptHidden(fd, "Password: ")
	if err != nil {
		return "", err
	}
	again, err := promptHidden(fd, "Repeat password: ")
	if err != nil {
		return "", err
	}
	if password != again {
		return "", errors.New("passwords do not match")
	}
	return password, nil
}

func promptHidden(fd int, prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	password, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(password), nil
}

// firstLine returns the first line of r without its line ending.
func firstLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
