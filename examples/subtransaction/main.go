// SPDX-FileCopyrightText: 2020 SAP SE
// SPDX-FileCopyrightText: 2021 SAP SE
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log"
	"math"

	"github.com/SAP/go-ase"
	"github.com/SAP/go-dblib/dsn"
)

// This example shows how to use nested transactions using the
// database/sql interface and the pure go driver.

func main() {
	if err := DoMain(); err != nil {
		log.Fatalf("transaction: %v", err)
	}
}

func DoMain() error {
	info, err := ase.NewInfoWithEnv()
	if err != nil {
		return fmt.Errorf("error reading DSN info from env: %w", err)
	}

	db, err := sql.Open("ase", dsn.FormatSimple(info))
	if err != nil {
		return fmt.Errorf("failed to open connection to database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("error closing db: %v", err)
		}
	}()

	conn, err := db.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("error getting conn: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("error closing conn: %v", err)
		}
	}()

	return conn.Raw(func(driverConn interface{}) error {
		if err := rawProcess(driverConn); err != nil {
			return fmt.Errorf("error in rawProcess: %w", err)
		}
		return nil
	})
}

func rawProcess(driverConn interface{}) error {
	conn, ok := driverConn.(*ase.Conn)
	if !ok {
		return errors.New("invalid driver, conn is not *github.com/SAP/go-ase.Conn")
	}

	fmt.Println("creating table subtransaction_tab")
	if _, _, err := conn.DirectExec(context.Background(), "if object_id('subtransaction_tab') is not null drop table subtransaction_tab"); err != nil {
		return fmt.Errorf("failed to drop table 'subtransaction_tab': %w", err)
	}

	if _, _, err := conn.DirectExec(context.Background(), "create table subtransaction_tab (a int, b char(30))"); err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}
	defer func() {
		if _, _, err := conn.DirectExec(context.Background(), "drop table subtransaction_tab"); err != nil {
			log.Printf("failed to drop table: %v", err)
		}
	}()

	fmt.Println("opening transaction")
	tx, err := conn.NewTransaction(context.Background(), driver.TxOptions{}, "outer")
	if err != nil {
		return fmt.Errorf("error creating transaction: %w", err)
	}

	// As the raw connection is used to create the transaction SQL
	// statements cannot be run through the tx struct as in the
	// transaction example.
	// Instead the statements must be executed through the conn.
	fmt.Println("inserting values into subtransaction_tab")
	if _, _, err = conn.DirectExec(context.Background(), "insert into subtransaction_tab (a, b) values (?, ?)", math.MaxInt32, "a string"); err != nil {
		return fmt.Errorf("failed to insert values: %w", err)
	}

	fmt.Println("reading table contents")
	if err := readTable(conn); err != nil {
		return fmt.Errorf("error reading table: %w", err)
	}

	fmt.Println("opening subtransaction")
	// Only the outermost transaction can have a name
	subTx, err := tx.NewTransaction(context.Background(), driver.TxOptions{})
	if err != nil {
		return fmt.Errorf("error opening subtransaction: %w", err)
	}

	// Now all SQL statements on conn are part of the subtransaction.
	if _, _, err := conn.DirectExec(context.Background(), "insert into subtransaction_tab (a, b) values (?, ?)", 1000, "another string"); err != nil {
		return fmt.Errorf("failed to insert values: %w", err)
	}

	fmt.Println("reading table contents with subtransaction changes")
	if err := readTable(conn); err != nil {
		return fmt.Errorf("error reading table: %w", err)
	}

	fmt.Println("rolling back subtransaction")
	if err := subTx.Rollback(); err != nil {
		return fmt.Errorf("error rolling back subtransaction: %w", err)
	}

	fmt.Println("reading table contents after subtransaction rollback")
	if err := readTable(conn); err != nil {
		return fmt.Errorf("error reading table: %w", err)
	}

	fmt.Println("committing transaction")
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
}

func readTable(conn *ase.Conn) error {
	stmt, err := conn.NewStmt(context.Background(), "", "select * from subtransaction_tab", true)
	if err != nil {
		return fmt.Errorf("error preparing statement: %w", err)
	}
	defer stmt.Close()

	rows, _, err := stmt.DirectExec(context.Background())
	if err != nil {
		return fmt.Errorf("error querying with prepared statement: %w", err)
	}

	values := []driver.Value{0, ""}
	for {
		if err := rows.Next(values); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("error reading row: %w", err)
		}

		fmt.Printf("a: %d\n", values[0])
		fmt.Printf("b: %s\n", values[1])
	}

	return nil
}
