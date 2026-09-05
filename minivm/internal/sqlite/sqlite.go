// Package sqlite provides only the SQLite calls this prototype needs. It links
// to the system/Nix SQLite library: no downloaded Go driver or bundled SQLite.
// Connections are serialized by the store; no C pointer escapes this package.
package sqlite

/*
#cgo LDFLAGS: -lsqlite3
#include <stdlib.h>
#include <sqlite3.h>
static int bind_text(sqlite3_stmt *s, int i, const char *p, int n) {
 return sqlite3_bind_text(s, i, p, n, SQLITE_TRANSIENT);
}
*/
import "C"
import (
	"errors"
	"fmt"
	"strings"
	"unsafe"
)

type DB struct{ p *C.sqlite3 }

func Open(path string) (*DB, error) {
	if strings.ContainsRune(path, 0) {
		return nil, errors.New("invalid database path")
	}
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	d := &DB{}
	rc := C.sqlite3_open_v2(p, &d.p, C.SQLITE_OPEN_READWRITE|C.SQLITE_OPEN_CREATE|C.SQLITE_OPEN_FULLMUTEX, nil)
	if rc != C.SQLITE_OK {
		err := d.err(rc)
		d.Close()
		return nil, err
	}
	if rc := C.sqlite3_busy_timeout(d.p, 5000); rc != C.SQLITE_OK {
		err := d.err(rc)
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) err(rc C.int) error {
	return fmt.Errorf("sqlite %d: %s", int(rc), C.GoString(C.sqlite3_errmsg(d.p)))
}

func (d *DB) Close() error {
	if d.p == nil {
		return nil
	}
	rc := C.sqlite3_close(d.p)
	if rc != C.SQLITE_OK {
		return d.err(rc)
	}
	d.p = nil
	return nil
}

func (d *DB) prepare(sql string, args []string) (*C.sqlite3_stmt, error) {
	if d.p == nil {
		return nil, errors.New("database closed")
	}
	if strings.ContainsRune(sql, 0) {
		return nil, errors.New("invalid SQL")
	}
	q := C.CString(sql)
	defer C.free(unsafe.Pointer(q))
	var stmt *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(d.p, q, -1, &stmt, nil); rc != C.SQLITE_OK {
		return nil, d.err(rc)
	}
	if stmt == nil {
		return nil, errors.New("empty statement")
	}
	if int(C.sqlite3_bind_parameter_count(stmt)) != len(args) {
		C.sqlite3_finalize(stmt)
		return nil, errors.New("parameter count mismatch")
	}
	for i, arg := range args {
		p := C.CString(arg)
		rc := C.bind_text(stmt, C.int(i+1), p, C.int(len(arg)))
		C.free(unsafe.Pointer(p))
		if rc != C.SQLITE_OK {
			err := d.err(rc)
			C.sqlite3_finalize(stmt)
			return nil, err
		}
	}
	return stmt, nil
}

func (d *DB) Exec(sql string, args ...string) error {
	stmt, err := d.prepare(sql, args)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	for {
		rc := C.sqlite3_step(stmt)
		switch rc {
		case C.SQLITE_DONE:
			return nil
		case C.SQLITE_ROW:
			continue
		default:
			return d.err(rc)
		}
	}
}

func (d *DB) Query(sql string, args ...string) ([][]string, error) {
	stmt, err := d.prepare(sql, args)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	rows := [][]string{}
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			return rows, nil
		}
		if rc != C.SQLITE_ROW {
			return nil, d.err(rc)
		}
		row := make([]string, int(C.sqlite3_column_count(stmt)))
		for i := range row {
			// Store queries explicitly coalesce nullable columns. Embedded NUL survives.
			p := C.sqlite3_column_text(stmt, C.int(i))
			n := C.sqlite3_column_bytes(stmt, C.int(i))
			if p != nil {
				row[i] = C.GoStringN((*C.char)(unsafe.Pointer(p)), n)
			}
		}
		rows = append(rows, row)
	}
}
