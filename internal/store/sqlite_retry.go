package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	"modernc.org/sqlite"
)

// SQLite permits one writer, including across host and MCP processes. IMMEDIATE
// transactions reserve that writer before reading; retrying a deferred snapshot
// upgrade cannot make its stale snapshot writable. Keep readers on the WAL pool.
func init() { sql.Register("lcroom-sqlite", retrySQLiteDriver{}) }

type retrySQLiteDriver struct{}

func (d retrySQLiteDriver) Open(name string) (driver.Conn, error) {
	return (retrySQLiteConnector{name: name}).Connect(context.Background())
}

func (d retrySQLiteDriver) OpenConnector(name string) (driver.Connector, error) {
	return retrySQLiteConnector{name: name}, nil
}

type retrySQLiteConnector struct{ name string }

func (c retrySQLiteConnector) Driver() driver.Driver { return retrySQLiteDriver{} }
func (c retrySQLiteConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := retrySQLite(ctx, func() (driver.Conn, error) {
		return (&sqlite.Driver{}).Open(c.name)
	})
	if err != nil {
		return nil, err
	}
	return &retrySQLiteConn{Conn: conn}, nil
}

type retrySQLiteConn struct{ driver.Conn }

func (c *retrySQLiteConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := retrySQLite(ctx, func() (driver.Tx, error) { return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts) })
	if err != nil {
		return nil, err
	}
	return &retrySQLiteTx{Tx: tx, conn: c, ctx: ctx}, nil
}

func (c *retrySQLiteConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return retrySQLite(ctx, func() (driver.Result, error) { return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args) })
}

func (c *retrySQLiteConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return retrySQLite(ctx, func() (driver.Rows, error) { return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args) })
}

type retrySQLiteTx struct {
	driver.Tx
	conn *retrySQLiteConn
	ctx  context.Context
}

func (t *retrySQLiteTx) Commit() error {
	// modernc's Tx.Commit rolls back immediately on BUSY. Execute COMMIT through
	// our connection instead so bounded retries retain the transaction, then
	// satisfy database/sql's clean-connection contract on terminal failure.
	_, err := t.conn.ExecContext(t.ctx, "COMMIT", nil)
	if err != nil {
		_ = t.Tx.Rollback()
	}
	return err
}

// Each attempt also has SQLite's five-second busy timeout. Four attempts bound
// a lock wait to about twenty seconds; caller cancellation stops the backoff.
// Do not retry BUSY_SNAPSHOT or LOCKED: those require releasing transaction or
// statement ownership, rather than replaying a statement in the same snapshot.
func retrySQLite[T any](ctx context.Context, run func() (T, error)) (T, error) {
	for attempt := 0; ; attempt++ {
		value, err := run()
		var coded interface{ Code() int }
		if err == nil || attempt == 3 || !errors.As(err, &coded) || (coded.Code() != 5 && coded.Code() != 261) {
			return value, err
		}
		timer := time.NewTimer((25 * time.Millisecond) << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return value, ctx.Err()
		case <-timer.C:
		}
	}
}
