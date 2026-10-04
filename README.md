# duosql

duosql is a high-performance Go ORM and query builder engine that unifies PostgreSQL and SQLite with zero boilerplate, builder-first syntax, and modern language idioms.

## Usage

### 1. Connecting to Database

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/thruqe/duosql"
)

func main() {
	// Connect to SQLite (Pure Go / CGO-free via modernc.org/sqlite)
	db, err := duosql.Open(duosql.DialectSQLite, "app.db?cache=shared",
		duosql.WithMaxOpenConns(25),
		duosql.WithMaxIdleConns(5),
		duosql.WithConnMaxLifetime(30*time.Minute),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Or connect to PostgreSQL via pgx stdlib adapter
	// pgDB, err := duosql.Open(duosql.DialectPostgres, "postgres://user:pass@localhost:5432/appdb")
}
```

### 2. DDL Schema Definition

```go
ctx := context.Background()

err := db.Schema().CreateTable("users", func(t *duosql.TableBuilder) {
	t.ID() // BIGSERIAL PRIMARY KEY (PG) or INTEGER PRIMARY KEY AUTOINCREMENT (SQLite)
	t.String("username", 100).NotNull().Unique()
	t.String("email", 255).NotNull()
	t.Int("age").Default(0)
	t.Bool("active").Default(true)
	t.JSON("profile").Nullable()
	t.Timestamp("created_at").NotNull()
}).IfNotExists().Exec(ctx)
```

### 3. Entity Models & Mapping

```go
type UserProfile struct {
	Theme string `json:"theme"`
	Role  string `json:"role"`
}

type User struct {
	ID        int64        `duo:"id,pk,auto"`
	Username  string       `duo:"username"`
	Email     string       `duo:"email"`
	Age       int          `duo:"age"`
	Active    bool         `duo:"active"`
	Profile   *UserProfile `duo:"profile,json"`
	CreatedAt time.Time    `duo:"created_at"`
}
```

### 4. Insert & UPSERT

```go
// Insert with RETURNING support
user, err := duosql.Insert[User](db).
	Values(&User{
		Username:  "thruqe",
		Email:     "thruqe@example.com",
		Age:       28,
		Active:    true,
		Profile:   &UserProfile{Theme: "matrix", Role: "engineer"},
		CreatedAt: time.Now(),
	}).
	Returning("*").
	One(ctx)

// Upsert: ON CONFLICT DO UPDATE
_, err = duosql.Insert[User](db).
	Values(user).
	OnConflictDoUpdate([]string{"username"}, []string{"email", "age"}).
	Exec(ctx)
```

### 5. Queries & Range-Over-Func Streaming Iterators

```go
// Fetch one record
admin, err := duosql.Select[User](db).
	Where(duosql.Col("username").Eq("thruqe")).
	One(ctx)

// Go 1.23+ range-over-func streaming iterators (Zero-allocation row processing)
query := duosql.Select[User](db).
	Where(
		duosql.Gte("age", 18),
		duosql.Eq("active", true),
	).
	OrderBy(duosql.Desc("created_at")).
	Limit(50)

for user, err := range query.Iter(ctx) {
	if err != nil {
		log.Printf("row stream error: %v", err)
		break
	}
	log.Printf("user: %s (%s)", user.Username, user.Email)
}
```

### 6. Transactions & Savepoints

```go
err := db.Transaction(ctx, func(tx *duosql.Tx) error {
	_, err := duosql.Insert[User](tx).Values(newUser).Exec(ctx)
	if err != nil {
		return err // Automatically rolls back
	}

	// Nested transactional savepoint
	if err := tx.Savepoint("sp1"); err != nil {
		return err
	}

	if err := riskyOperation(tx); err != nil {
		_ = tx.RollbackTo("sp1") // Reverts only changes made after sp1
	}

	return nil // Automatically commits
})
```

### 7. Soft Deletes & Atomic Counter Updates

```go
type Post struct {
	ID        int64      `duo:"id,pk,auto"`
	Title     string     `duo:"title"`
	Likes     int64      `duo:"likes"`
	DeletedAt *time.Time `duo:"deleted_at,soft_delete"`
}

// Atomic increment & decrement without race conditions
_, err = duosql.Update[Post](db).Where(duosql.Eq("id", 1)).Inc("likes", 1).Exec(ctx)

// Soft delete (sets deleted_at = CURRENT_TIMESTAMP under the hood)
_, err = duosql.Delete[Post](db).Where(duosql.Eq("id", 1)).Exec(ctx)

// Standard queries automatically exclude soft-deleted records
// Retrieve soft-deleted records explicitly:
posts, err := duosql.Select[Post](db).WithTrashed().All(ctx)
onlyTrash, err := duosql.Select[Post](db).OnlyTrashed().All(ctx)

// Force permanent deletion:
_, err = duosql.Delete[Post](db).ForceDelete().Where(duosql.Eq("id", 1)).Exec(ctx)
```

### 8. Subqueries, Aggregations, & JSON Extraction

```go
// Subqueries
sub := duosql.Select[User](db, "id").Where(duosql.Gt("age", 25))
activeOrders, err := duosql.Select[Order](db).Where(duosql.InSubquery("user_id", sub)).All(ctx)

// Aggregations & Pluck
totalAge, err := duosql.Select[User](db).Sum(ctx, "age")
usernames, err := duosql.Pluck[string](duosql.Select[User](db), ctx, "username")

// JSON Path Filtering (PG `col->>'role'` / SQLite `json_extract(col, '$.role')`)
admins, err := duosql.Select[User](db).
	Where(duosql.JSONExtract("profile", "role").Eq("admin")).
	All(ctx)
```

## Features

- Unified PostgreSQL & SQLite Dialects (`$1` vs `?` placeholders, quoting, type coercions)
- Generic Type-Safe Query Builders (`Select[T]`, `Insert[T]`, `Update[T]`, `Delete[T]`)
- Go 1.23+ Range-Over-Func Streaming (`iter.Seq2[*T, error]`) for memory-efficient iteration
- Full Schema DDL Builder with Migrations, Alterations (`AlterTable`), Foreign Keys, and Indexes
- Native UPSERT (`ON CONFLICT DO UPDATE / DO NOTHING`) across both engines
- Expression & Predicate Builder (`Eq`, `Neq`, `Gt`, `Gte`, `Lt`, `Lte`, `In`, `NotIn`, `Between`, `Like`, `ILike`, `And`, `Or`, `Not`, `Raw`)
- Subqueries & Existence Checks (`InSubquery`, `NotInSubquery`, `ExistsSubquery`, `NotExistsSubquery`)
- Native JSON Path Extraction (`JSONExtract("col", "path").Eq(...)`) across PostgreSQL JSONB and SQLite JSON
- Automatic Soft Deletes (`duo:"deleted_at,soft_delete"`, `WithTrashed()`, `OnlyTrashed()`, `ForceDelete()`)
- Atomic Field Increments & Decrements (`Inc`, `Dec`) and Raw Update Expressions
- Pagination & Chunked Batch Insertions (`Paginate(page, size)`, `InsertInBatches(ctx, size)`)
- Aggregation Helpers & Value Plucking (`Sum`, `Avg`, `Min`, `Max`, `Pluck[V]`)
- Clause-level `RETURNING` support for SQLite 3.35+ and PostgreSQL
- Reflection Metadata Cache & Automatic JSON Serialization
- Atomic Transactions with Automatic Rollback and Named Savepoints
- Query Telemetry & Middleware Hooks (`BeforeQuery`, `AfterQuery`, latency metrics)
- Pure Go / Zero CGO SQLite support (`modernc.org/sqlite`) & Native PGX v5 stdlib

## Contributions

If you want to help make this project better, please take the time to read this contribution [doc](./docs/CONTRIBUTING.md) and [fork](https://github.com/thruqe/duosql/fork) this repository. Then open a pull request with your changes.

If you are using any AI agent for assistance, please refer to the [AGENTS documentation](./docs/AGENTS.md) Guide.

## Acknowledgements

duosql wouldn't have been possible without these open source, community and passion-driven projects: [modernc.org/sqlite](https://gitlab.com/cznic/sqlite), [pgx](https://github.com/jackc/pgx), and the broader Go ecosystem.

## Licensing

This project is open source, see the [LICENSE](./LICENSE) file for full details.

## Support this project

If you found this project to be useful in anyway, please consider sending a tip.

<a href="https://etherscan.io/address/0xfA1617fC3aeA4B2BC6Fb3928aA2cAE2fB97ba0ED"><img src="https://cdn-icons-png.flaticon.com/128/15301/15301597.png" width="24" height="24" alt="Donate ETH" /></a> `0xfA1617fC3aeA4B2BC6Fb3928aA2cAE2fB97ba0ED`
