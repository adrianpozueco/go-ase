// SPDX-FileCopyrightText: 2020 SAP SE
// SPDX-FileCopyrightText: 2021 SAP SE
//
// SPDX-License-Identifier: Apache-2.0

package ase

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"

	"github.com/SAP/go-dblib/tds"
)

// Interface satisfaction checks.
var (
	_ driver.Rows                           = (*Rows)(nil)
	_ driver.RowsNextResultSet              = (*Rows)(nil)
	_ driver.RowsColumnTypeLength           = (*Rows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*Rows)(nil)
)

// Rows implements the driver.Rows interface.
type Rows struct {
	Conn   *Conn
	RowFmt *tds.RowFmtPackage

	hasNextResultSet bool
}

// Columns implements the driver.Rows interface.
func (rows Rows) Columns() []string {
	if rows.RowFmt == nil {
		return []string{}
	}

	// TODO ignore hidden columns
	response := make([]string, len(rows.RowFmt.Fmts))

	for i, fieldFmt := range rows.RowFmt.Fmts {
		// TODO check if RowFmt is wide and contains column label,
		// catalogue, schema, table
		response[i] = fieldFmt.Name()
	}

	return response
}

// Close implements the driver.Rows interface.
func (rows *Rows) Close() error {
	for {
		if err := rows.NextResultSet(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("go-ase: error consuming result sets: %w", err)
		}
	}

	return nil
}

// Next implements the driver.Rows interface.
func (rows *Rows) Next(dst []driver.Value) error {
	if rows.RowFmt == nil && len(dst) == 0 {
		return io.EOF
	}

	_, err := rows.Conn.Channel.NextPackageUntil(context.Background(), true,
		func(pkg tds.Package) (bool, error) {
			switch typed := pkg.(type) {
			case *tds.RowPackage:
				if len(dst) != len(typed.DataFields) {
					return true, fmt.Errorf("go-ase: received invalid number of destinations, expecting %d destinations, got %d", len(typed.DataFields), len(dst))
				}
				for i := range typed.DataFields {
					dst[i] = typed.DataFields[i].Value()
				}
				return true, nil
			case *tds.RowFmtPackage:
				rows.RowFmt = typed
				rows.hasNextResultSet = true
				return false, io.EOF
			case *tds.OrderByPackage:
				return false, nil
			case *tds.DonePackage:
				ok, err := handleDonePackage(typed)
				if err != nil {
					return true, fmt.Errorf("go-ase: %w", err)
				}

				return ok, nil
			case *tds.ReturnStatusPackage:
				if typed.ReturnValue != 0 {
					return true, fmt.Errorf("go-ase: query failed with return status %d", typed.ReturnValue)
				}
				return false, nil
			default:
				return true, fmt.Errorf("unhandled package type %T: %v", pkg, pkg)
			}
		},
	)

	if err != nil {
		// database/sql expects only an io.EOF - it doesn't check with
		// errors.Is.
		if errors.Is(err, io.EOF) {
			return io.EOF
		}
		return fmt.Errorf("go-ase: error reading next row package: %w", err)
	}

	return nil
}

// HasNextResultSet implements the driver.RowsNextResultSet interface.
func (rows *Rows) HasNextResultSet() bool {
	if !rows.hasNextResultSet {
		return false
	}
	rows.hasNextResultSet = false
	return true
}

// NextResultSet implements the driver.RowsNextResultSet interface.
func (rows *Rows) NextResultSet() error {
	// discard all RowPackage until either end of communication or next
	// RowFmtPackage
	_, err := rows.Conn.Channel.NextPackageUntil(context.Background(), false,
		func(pkg tds.Package) (bool, error) {
			switch typed := pkg.(type) {
			case *tds.RowFmtPackage:
				rows.RowFmt = typed
				rows.hasNextResultSet = true
				return false, nil
			case *tds.RowPackage, *tds.OrderByPackage:
				return true, nil
			case *tds.DonePackage:
				if typed.Status&tds.TDS_DONE_MORE == tds.TDS_DONE_MORE {
					return false, nil
				}
				return true, fmt.Errorf("go-ase: no next result set: %w", io.EOF)
			default:
				return false, fmt.Errorf("unhandled package type %T: %v", pkg, pkg)
			}
		},
	)

	if err != nil {
		if errors.Is(err, tds.ErrNoPackageReady) || errors.Is(err, io.EOF) {
			return io.EOF
		}
		return fmt.Errorf("go-ase: error reading next package: %w", err)
	}

	return nil
}

// ColumnTypeLength implements the driver.RowsColumnTypeLength interface.
func (rows Rows) ColumnTypeLength(index int) (int64, bool) {
	if index >= len(rows.RowFmt.Fmts) {
		return 0, false
	}
	return rows.RowFmt.Fmts[index].MaxLength(), true
}

// ColumnTypeDatabaseTypeName implements the
// driver.RowsColumnTypeDatabaseTypeName interface.
func (rows Rows) ColumnTypeDatabaseTypeName(index int) string {
	if index >= len(rows.RowFmt.Fmts) {
		return ""
	}
	return string(rows.RowFmt.Fmts[index].DataType())
}
