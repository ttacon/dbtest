package dbtest

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// An Executor is a convenienve interface that covers both
// sql.DB and sql.Tx from database/sql. It can be used to
// allow either of those to be used interchangeable (bad idea),
// but primarily it is used to transparently swap out a real db
// connection for a deterministic pseudo-Executor for testing.
type Executor interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (SQLRows, error)
	QueryRow(query string, args ...interface{}) SQLRow
}

// A TestExecutor can be used as an Executor, but can have
// the results it will return predefined.
type TestExecutor interface {
	Executor
	RegisterExec(id string, s sql.Result) TestExecutor
	RegisterQuery(id string, s SQLRows) TestExecutor
	RegisterQueryRow(id string, s SQLRow) TestExecutor
}

// For being able to convert standard library executors to dbtest executors.
type StdLibExecutor interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// A convenience method for transforming a StdLibExecutor to an Executor.
func ToExecutor(e StdLibExecutor) Executor {
	return &executor{e}
}

type executor struct {
	e StdLibExecutor
}

func (e *executor) Exec(query string, args ...interface{}) (sql.Result, error) {
	return e.e.Exec(query, args...)
}

func (e *executor) Query(query string, args ...interface{}) (SQLRows, error) {
	return e.e.Query(query, args...)
}

func (e *executor) QueryRow(query string, args ...interface{}) SQLRow {
	return e.e.QueryRow(query, args...)
}

// A convenience wrapper for sql.Row - as you can't really mock out
// an "empty" struct.
type SQLRow interface {
	SQLScannable
}

// Because we're going to reuse it...
type SQLScannable interface {
	Scan(dest ...interface{}) error
}

// A convenienve wrapper for sql.Rows - as you can't really mock "filtered
// or unexported fields."
type SQLRows interface {
	Close() error
	Columns() ([]string, error)
	Err() error
	Next() bool
	SQLScannable
}

type testExecutor struct {
	results map[string]sql.Result
	rows    map[string]SQLRows
	row     map[string]SQLRow
}

type sqlResult struct {
	lastInsertID, rowsAffected     int64
	lastInsertErr, rowsAffectedErr error
}

// Create a sql.Result that "had" the given lastInsertID and affected
// the given number of rows.
func SQLResult(lastInsertID, rowsAffected int64) sql.Result {
	return sqlResult{
		lastInsertID: lastInsertID,
		rowsAffected: rowsAffected,
	}
}

// Same as SQLResult(), except we can specify certai errors to be
// returned when either LastInsertId() or RowsAffected() are called.
func SQLResultWErrors(lID, rAff int64, lIDErr, rAffErr error) sql.Result {
	return sqlResult{
		lastInsertID:    lID,
		rowsAffected:    rAff,
		lastInsertErr:   lIDErr,
		rowsAffectedErr: rAffErr,
	}
}

func (s sqlResult) LastInsertId() (int64, error) {
	return s.lastInsertID, s.lastInsertErr
}

func (s sqlResult) RowsAffected() (int64, error) {
	return s.rowsAffected, s.rowsAffectedErr
}

// Returns a TestExecutor which we can attach our predetermined results to.
func NewTestExecutor() TestExecutor {
	return &testExecutor{
		results: make(map[string]sql.Result),
		rows:    make(map[string]SQLRows),
		row:     make(map[string]SQLRow),
	}
}

func (t *testExecutor) Exec(query string, args ...interface{}) (sql.Result, error) {
	resultID := fmt.Sprintf(strings.Replace(query, "?", "%v", -1), args...)
	if r, ok := t.results[resultID]; ok {
		return r, nil
	}
	return nil, errors.New("invalid query/args combo, not registered with TestExecutor")
}

func (t *testExecutor) Query(query string, args ...interface{}) (SQLRows, error) {
	queryID := fmt.Sprintf(strings.Replace(query, "?", "%v", -1), args...)
	if q, ok := t.rows[queryID]; ok {
		return q, nil
	}
	return nil, errors.New("invalid query/args combo, not registered with TestExecutor")
}

func (t *testExecutor) QueryRow(query string, args ...interface{}) SQLRow {
	queryRowID := fmt.Sprintf(strings.Replace(query, "?", "%v", -1), args...)
	if q, ok := t.row[queryRowID]; ok {
		return q
	}
	return nil
}

func (t *testExecutor) RegisterExec(id string, s sql.Result) TestExecutor {
	t.results[id] = s
	return t
}
func (t *testExecutor) RegisterQuery(id string, s SQLRows) TestExecutor {
	t.rows[id] = s
	return t
}
func (t *testExecutor) RegisterQueryRow(id string, s SQLRow) TestExecutor {
	t.row[id] = s
	return t
}

// NewSQLRow creates a new SQLRow with the given fields.
func NewSQLRow(fields []interface{}) SQLRow {
	return &sqlRow{
		fields: fields,
	}
}

type sqlRow struct {
	fields []interface{}
}

func (s *sqlRow) Scan(dest ...interface{}) error {
	if s == nil {
		// TODO(ttacon): better error message
		return errors.New("no values were found")
	}
	if len(dest) != len(s.fields) {
		return errors.New(
			fmt.Sprintf("expected %d destination arguments in Scan, not %d",
				len(s.fields), len(dest)))
	}

	for i, val := range s.fields {
		if err := convertAssign(dest[i], val); err != nil {
			return err
		}
	}
	return nil
}

// NewSQLRows creates a new SQLRows with the given column names and given
// "SQLRow"s.
func NewSQLRows(cols []string, rows []SQLRow) SQLRows {
	return &sqlRows{
		columns: cols,
		currRow: -1,
		rows:    rows,
	}
}

type sqlRows struct {
	columns []string
	currRow int
	rows    []SQLRow
}

func (s *sqlRows) Close() error {
	// TODO(ttacon): this should probably set currRow = len(colums)
	return nil
}

func (s *sqlRows) Columns() ([]string, error) {
	return s.columns, nil
}

func (s *sqlRows) Err() error {
	return nil
}

func (s *sqlRows) Next() bool {
	s.currRow++
	return s.currRow < len(s.rows)
}

func (s *sqlRows) Scan(dest ...interface{}) error {
	if s.currRow < 0 {
		return errors.New("must call Next() before scan")
	} else if s.currRow >= len(s.rows) {
		return errors.New("no more rows to scan into")
	}

	return s.rows[s.currRow].Scan(dest...)
}

// An OrderedTestExecutor is a simpler TestExecutor - it simple returns
// sql.Results, "SQLRow"s and SQLRows in the order they were provided.
type OrderedTestExecutor interface {
	Executor
	RegisterExec(sql.Result) OrderedTestExecutor
	RegisterQuery(SQLRows) OrderedTestExecutor
	RegisterQueryRow(SQLRow) OrderedTestExecutor
}

// NewOrderedTestExecutor returns a blank OrderedTestExecutor.
func NewOrderedTestExecutor() OrderedTestExecutor {
	return &orderedTestExctr{}
}

type orderedTestExctr struct {
	results []sql.Result
	rows    []SQLRows
	row     []SQLRow
}

func (o *orderedTestExctr) Exec(query string, args ...interface{}) (sql.Result, error) {
	if len(o.results) == 0 {
		// TODO(ttacon): better error message?
		return nil, errors.New("no results left")
	}
	next := o.results[0]
	o.results = o.results[1:]
	return next, nil
}

func (o *orderedTestExctr) Query(query string, args ...interface{}) (SQLRows, error) {
	if len(o.rows) == 0 {
		// TODO(ttacon): better error message?
		return nil, errors.New("no rows left")
	}
	next := o.rows[0]
	o.rows = o.rows[1:]
	return next, nil
}

func (o *orderedTestExctr) QueryRow(query string, args ...interface{}) SQLRow {
	if len(o.row) == 0 {
		// TODO(ttacon): better error message?
		return nil
	}
	next := o.row[0]
	o.row = o.row[1:]
	return next
}

func (o *orderedTestExctr) RegisterExec(r sql.Result) OrderedTestExecutor {
	o.results = append(o.results, r)
	return o
}

func (o *orderedTestExctr) RegisterQuery(r SQLRows) OrderedTestExecutor {
	o.rows = append(o.rows, r)
	return o
}

func (o *orderedTestExctr) RegisterQueryRow(r SQLRow) OrderedTestExecutor {
	o.row = append(o.row, r)
	return o
}
