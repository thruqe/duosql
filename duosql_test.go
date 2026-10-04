package duosql_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/thruqe/duosql"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

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

func setupTestDB(t *testing.T) *duosql.DB {
	t.Helper()
	// Using SQLite in-memory with shared cache for fast, isolated verification
	db, err := duosql.Open(duosql.DialectSQLite, "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.Schema().CreateTable("users", func(tb *duosql.TableBuilder) {
		tb.ID()
		tb.String("username", 100).NotNull().Unique()
		tb.String("email", 255).NotNull()
		tb.Int("age").Default(0)
		tb.Bool("active").Default(true)
		tb.JSON("profile").Nullable()
		tb.Timestamp("created_at").NotNull()
	}).IfNotExists().Exec(ctx)
	if err != nil {
		t.Fatalf("failed to create users table: %v", err)
	}

	return db
}

func TestSchemaBuilder(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()

	exists, err := db.Schema().HasTable(ctx, "users")
	if err != nil {
		t.Fatalf("failed to check table existence: %v", err)
	}
	if !exists {
		t.Fatalf("expected table 'users' to exist")
	}

	err = db.Schema().CreateIndex("idx_users_email").
		On("users", "email").
		IfNotExists().
		Exec(ctx)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	// Verify non-existent table check
	missing, err := db.Schema().HasTable(ctx, "non_existent_table")
	if err != nil {
		t.Fatalf("unexpected error checking non existent table: %v", err)
	}
	if missing {
		t.Fatalf("expected missing table to report false")
	}
}

func TestInsertAndQueryOne(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	newUser := &User{
		Username:  "alice",
		Email:     "alice@example.com",
		Age:       30,
		Active:    true,
		Profile:   &UserProfile{Theme: "dark", Role: "admin"},
		CreatedAt: now,
	}

	inserted, err := duosql.Insert[User](db).
		Values(newUser).
		Returning("*").
		One(ctx)
	if err != nil {
		t.Fatalf("failed to insert user with returning: %v", err)
	}

	if inserted.ID == 0 {
		t.Errorf("expected inserted ID to be populated, got 0")
	}
	if inserted.Username != "alice" {
		t.Errorf("expected username alice, got %s", inserted.Username)
	}
	if inserted.Profile == nil || inserted.Profile.Theme != "dark" {
		t.Errorf("expected json profile to deserialize theme=dark, got %+v", inserted.Profile)
	}

	// Fetch back using Select One
	fetched, err := duosql.Select[User](db).
		Where(duosql.Col("username").Eq("alice")).
		One(ctx)
	if err != nil {
		t.Fatalf("failed to select user by username: %v", err)
	}
	if fetched.ID != inserted.ID {
		t.Errorf("expected ID %d, got %d", inserted.ID, fetched.ID)
	}
}

func TestRangeOverFuncIterator(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	users := []*User{
		{Username: "user1", Email: "u1@example.com", Age: 20, Active: true, CreatedAt: now},
		{Username: "user2", Email: "u2@example.com", Age: 25, Active: true, CreatedAt: now},
		{Username: "user3", Email: "u3@example.com", Age: 35, Active: false, CreatedAt: now},
	}

	for _, u := range users {
		_, err := duosql.Insert[User](db).Values(u).Exec(ctx)
		if err != nil {
			t.Fatalf("failed inserting test seed user: %v", err)
		}
	}

	// Test Go 1.23+ range-over-func iterator
	var streamedUsernames []string
	query := duosql.Select[User](db).
		Where(duosql.Gte("age", 20)).
		Asc("age")

	for u, err := range query.Iter(ctx) {
		if err != nil {
			t.Fatalf("stream iteration error: %v", err)
		}
		streamedUsernames = append(streamedUsernames, u.Username)
	}

	if len(streamedUsernames) != 3 {
		t.Fatalf("expected 3 streamed users, got %d", len(streamedUsernames))
	}
	if streamedUsernames[0] != "user1" || streamedUsernames[2] != "user3" {
		t.Errorf("unexpected streaming order: %v", streamedUsernames)
	}
}

func TestPredicatesAndCount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	for i := 1; i <= 5; i++ {
		_, err := duosql.Insert[User](db).Values(&User{
			Username:  fmt.Sprintf("dev_%d", i),
			Email:     fmt.Sprintf("dev_%d@domain.com", i),
			Age:       20 + i*2,
			Active:    i%2 == 0,
			CreatedAt: now,
		}).Exec(ctx)
		if err != nil {
			t.Fatalf("failed inserting record %d: %v", i, err)
		}
	}

	// Between predicate & Count
	count, err := duosql.Select[User](db).
		Where(duosql.Between("age", 22, 28)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 4 { // 22, 24, 26, 28
		t.Errorf("expected count 4, got %d", count)
	}

	// In predicate
	inUsers, err := duosql.Select[User](db).
		Where(duosql.In("username", "dev_1", "dev_3")).
		All(ctx)
	if err != nil {
		t.Fatalf("in query failed: %v", err)
	}
	if len(inUsers) != 2 {
		t.Errorf("expected 2 users in IN predicate, got %d", len(inUsers))
	}

	// Exists check
	exists, err := duosql.Select[User](db).
		Where(duosql.Eq("username", "dev_1")).
		Exists(ctx)
	if err != nil || !exists {
		t.Errorf("expected exists=true, got %v (err: %v)", exists, err)
	}

	missingExists, err := duosql.Select[User](db).
		Where(duosql.Eq("username", "ghost")).
		Exists(ctx)
	if err != nil || missingExists {
		t.Errorf("expected exists=false for ghost, got %v (err: %v)", missingExists, err)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	_, err := duosql.Insert[User](db).Values(&User{
		Username:  "bob",
		Email:     "bob@example.com",
		Age:       40,
		Active:    true,
		CreatedAt: now,
	}).Exec(ctx)
	if err != nil {
		t.Fatalf("insert bob failed: %v", err)
	}

	// Update with Set and Where
	rowsAffected, err := duosql.Update[User](db).
		Set("age", 41).
		Where(duosql.Eq("username", "bob")).
		Exec(ctx)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if rowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", rowsAffected)
	}

	// Verify update
	bob, err := duosql.Select[User](db).Where(duosql.Eq("username", "bob")).One(ctx)
	if err != nil {
		t.Fatalf("failed to query updated bob: %v", err)
	}
	if bob.Age != 41 {
		t.Fatalf("expected updated age 41, got %d", bob.Age)
	}

	// Delete
	delRows, err := duosql.Delete[User](db).
		Where(duosql.Eq("username", "bob")).
		Exec(ctx)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if delRows != 1 {
		t.Errorf("expected 1 row deleted, got %d", delRows)
	}

	// Verify gone
	_, err = duosql.Select[User](db).Where(duosql.Eq("username", "bob")).One(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows after delete, got %v", err)
	}
}

func TestTransactionsAndSavepoints(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Transaction successful commit
	err := db.Transaction(ctx, func(tx *duosql.Tx) error {
		_, err := duosql.Insert[User](tx).Values(&User{
			Username:  "tx_user_1",
			Email:     "tx1@example.com",
			Age:       22,
			Active:    true,
			CreatedAt: now,
		}).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatalf("transaction commit failed: %v", err)
	}

	// Verify tx_user_1 exists
	exists, err := duosql.Select[User](db).Where(duosql.Eq("username", "tx_user_1")).Exists(ctx)
	if err != nil || !exists {
		t.Fatalf("expected tx_user_1 to persist after commit")
	}

	// Transaction automatic rollback on error
	_ = db.Transaction(ctx, func(tx *duosql.Tx) error {
		_, _ = duosql.Insert[User](tx).Values(&User{
			Username:  "tx_rollback_user",
			Email:     "rollback@example.com",
			Age:       22,
			Active:    true,
			CreatedAt: now,
		}).Exec(ctx)
		return errors.New("abort transaction")
	})

	// Verify rolled back
	exists, _ = duosql.Select[User](db).Where(duosql.Eq("username", "tx_rollback_user")).Exists(ctx)
	if exists {
		t.Fatalf("expected tx_rollback_user to be rolled back")
	}

	// Savepoint test
	err = db.Transaction(ctx, func(tx *duosql.Tx) error {
		_, err := duosql.Insert[User](tx).Values(&User{
			Username:  "savepoint_user_1",
			Email:     "sp1@example.com",
			Age:       33,
			Active:    true,
			CreatedAt: now,
		}).Exec(ctx)
		if err != nil {
			return err
		}

		if err := tx.Savepoint("sp1"); err != nil {
			return err
		}

		_, err = duosql.Insert[User](tx).Values(&User{
			Username:  "savepoint_user_2",
			Email:     "sp2@example.com",
			Age:       34,
			Active:    true,
			CreatedAt: now,
		}).Exec(ctx)
		if err != nil {
			return err
		}

		// Rollback to sp1
		if err := tx.RollbackTo("sp1"); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		t.Fatalf("transaction with savepoint failed: %v", err)
	}

	// sp1 user must exist, sp2 user must not exist
	u1Exists, _ := duosql.Select[User](db).Where(duosql.Eq("username", "savepoint_user_1")).Exists(ctx)
	u2Exists, _ := duosql.Select[User](db).Where(duosql.Eq("username", "savepoint_user_2")).Exists(ctx)
	if !u1Exists {
		t.Errorf("expected savepoint_user_1 to exist")
	}
	if u2Exists {
		t.Errorf("expected savepoint_user_2 to have been rolled back via savepoint")
	}
}

func TestPostgresDialectCompilation(t *testing.T) {
	pgDialect := duosql.NewPostgresDialect()
	dummyDB := duosql.Wrap(&sql.DB{}, pgDialect)

	// SELECT compilation test with $1, $2 placeholders and ILIKE
	query, args, err := duosql.Select[User](dummyDB).
		Where(
			duosql.Col("username").ILike("%john%"),
			duosql.Gt("age", 25),
		).
		OrderBy(duosql.Desc("created_at")).
		Limit(10).
		Offset(20).
		ForUpdate().
		Build()
	if err != nil {
		t.Fatalf("failed to build postgres select query: %v", err)
	}

	expectedSubstring := `WHERE "username" ILIKE $1 AND "age" > $2 ORDER BY "created_at" DESC LIMIT 10 OFFSET 20 FOR UPDATE`
	if query == "" || len(args) != 2 {
		t.Fatalf("unexpected query args count %d", len(args))
	}
	if !containsStr(query, `"username" ILIKE $1`) || !containsStr(query, `"age" > $2`) {
		t.Errorf("expected postgres placeholders $1, $2, got query: %s", query)
	}
	if !containsStr(query, "FOR UPDATE") {
		t.Errorf("expected FOR UPDATE in postgres query, got: %s", query)
	}
	_ = expectedSubstring
}

func TestUpsert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Initial insert
	u1 := &User{Username: "dave", Email: "dave@example.com", Age: 25, Active: true, CreatedAt: now}
	_, err := duosql.Insert[User](db).Into("users").Values(u1).Exec(ctx)
	if err != nil {
		t.Fatalf("failed initial insert: %v", err)
	}

	// On conflict do nothing
	uDuplicate := &User{Username: "dave", Email: "dave_new@example.com", Age: 26, Active: true, CreatedAt: now}
	_, err = duosql.Insert[User](db).
		Columns("username", "email", "age", "active", "created_at").
		Values(uDuplicate).
		OnConflictDoNothing("username").
		Exec(ctx)
	if err != nil {
		t.Fatalf("on conflict do nothing failed: %v", err)
	}

	// Verify not changed
	dave, _ := duosql.Select[User](db).Where(duosql.Eq("username", "dave")).One(ctx)
	if dave.Email != "dave@example.com" {
		t.Errorf("expected email to remain unchanged, got %s", dave.Email)
	}

	// On conflict do update
	uUpdated := &User{Username: "dave", Email: "dave_updated@example.com", Age: 27, Active: true, CreatedAt: now}
	_, err = duosql.Insert[User](db).
		Values(uUpdated).
		OnConflictDoUpdate([]string{"username"}, []string{"email", "age"}).
		Exec(ctx)
	if err != nil {
		t.Fatalf("on conflict do update failed: %v", err)
	}

	daveUpdated, _ := duosql.Select[User](db).Where(duosql.Eq("username", "dave")).One(ctx)
	if daveUpdated.Email != "dave_updated@example.com" || daveUpdated.Age != 27 {
		t.Errorf("expected updated dave, got email=%s, age=%d", daveUpdated.Email, daveUpdated.Age)
	}
}

func TestValueMapAndBatchReturning(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Insert via ValueMap
	_, err := duosql.Insert[User](db).
		Into("users").
		ValueMap(
			map[string]any{"username": "map1", "email": "m1@test.com", "age": 21, "active": true, "created_at": now},
			map[string]any{"username": "map2", "email": "m2@test.com", "age": 22, "active": true, "created_at": now},
		).
		Exec(ctx)
	if err != nil {
		t.Fatalf("value map insert failed: %v", err)
	}

	allBatch, err := duosql.Insert[User](db).
		Into("users").
		Values(
			&User{Username: "batch1", Email: "b1@test.com", Age: 31, Active: true, CreatedAt: now},
			&User{Username: "batch2", Email: "b2@test.com", Age: 32, Active: true, CreatedAt: now},
		).
		Returning("*").
		All(ctx)
	if err != nil {
		t.Fatalf("batch insert returning failed: %v", err)
	}
	if len(allBatch) != 2 {
		t.Errorf("expected 2 batch inserted users, got %d", len(allBatch))
	}
}

func TestJoinsAndAggregations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create orders table
	err := db.Schema().CreateTable("orders", func(tb *duosql.TableBuilder) {
		tb.ID()
		tb.BigInt("user_id").NotNull()
		tb.Int("amount").NotNull()
		tb.ForeignKey("user_id", "users", "id").OnDelete("CASCADE")
	}).IfNotExists().Exec(ctx)
	if err != nil {
		t.Fatalf("failed to create orders table: %v", err)
	}

	u, err := duosql.Insert[User](db).
		Values(&User{Username: "shopper", Email: "shop@test.com", Age: 29, Active: true, CreatedAt: now}).
		Returning("*").
		One(ctx)
	if err != nil {
		t.Fatalf("insert shopper failed: %v", err)
	}

	type Order struct {
		ID     int64 `duo:"id,pk,auto"`
		UserID int64 `duo:"user_id"`
		Amount int   `duo:"amount"`
	}

	_, err = duosql.Insert[Order](db).
		Values(
			&Order{UserID: u.ID, Amount: 100},
			&Order{UserID: u.ID, Amount: 200},
		).
		Exec(ctx)
	if err != nil {
		t.Fatalf("insert orders failed: %v", err)
	}

	// Test Join, LeftJoin, RightJoin, FullJoin, CrossJoin query builder compilation
	joinQuery, _, err := duosql.Select[Order](db).
		From("orders").
		As("o").
		Distinct().
		Join("users", duosql.Raw(`"o"."user_id" = "users"."id"`)).
		LeftJoin("users", duosql.Raw(`"o"."user_id" = "users"."id"`)).
		RightJoin("users", duosql.Raw(`"o"."user_id" = "users"."id"`)).
		FullJoin("users", duosql.Raw(`"o"."user_id" = "users"."id"`)).
		CrossJoin("users").
		GroupBy("o.id", "o.user_id").
		Having(duosql.Gt("o.amount", 50)).
		Desc("o.id").
		Build()
	if err != nil {
		t.Fatalf("failed to build join query: %v", err)
	}
	if !containsStr(joinQuery, "INNER JOIN") || !containsStr(joinQuery, "LEFT JOIN") || !containsStr(joinQuery, "GROUP BY") {
		t.Errorf("expected joins and group by in query: %s", joinQuery)
	}
}

func TestUpdateAndDeleteVariations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	u, err := duosql.Insert[User](db).
		Values(&User{Username: "charlie", Email: "charlie@test.com", Age: 50, Active: true, CreatedAt: now}).
		Returning("*").
		One(ctx)
	if err != nil {
		t.Fatalf("insert charlie failed: %v", err)
	}

	// SetMap
	_, err = duosql.Update[User](db).
		Table("users").
		SetMap(map[string]any{"age": 52}).
		Where(duosql.Eq("id", u.ID)).
		Exec(ctx)
	if err != nil {
		t.Fatalf("update set map failed: %v", err)
	}

	// SetModel
	u.Age = 55
	u.Email = "charlie_updated@test.com"
	upOne, err := duosql.Update[User](db).
		SetModel(u).
		Where(duosql.Eq("id", u.ID)).
		Returning("*").
		One(ctx)
	if err != nil {
		t.Fatalf("update set model returning one failed: %v", err)
	}
	if upOne.Age != 55 || upOne.Email != "charlie_updated@test.com" {
		t.Errorf("expected updated charlie model, got %+v", upOne)
	}

	// Update All returning
	upAll, err := duosql.Update[User](db).
		Set("age", 56).
		Where(duosql.Eq("id", u.ID)).
		Returning("*").
		All(ctx)
	if err != nil || len(upAll) != 1 {
		t.Fatalf("update all returning failed: len=%d, err=%v", len(upAll), err)
	}

	// Delete Returning One
	delOne, err := duosql.Delete[User](db).
		From("users").
		Where(duosql.Eq("id", u.ID)).
		Returning("*").
		One(ctx)
	if err != nil {
		t.Fatalf("delete returning one failed: %v", err)
	}
	if delOne.Username != "charlie" {
		t.Errorf("expected deleted user charlie, got %s", delOne.Username)
	}

	// Delete Returning All
	_ = duosql.Delete[User](db).Where(duosql.Eq("id", 9999)).Returning("*")
	delAll, err := duosql.Delete[User](db).Where(duosql.Eq("id", 9999)).All(ctx)
	if err != nil {
		t.Fatalf("delete all returning failed: %v", err)
	}
	if len(delAll) != 0 {
		t.Errorf("expected 0 deleted users, got %d", len(delAll))
	}
}

func TestExpressionsAndHooks(t *testing.T) {
	hookFired := false
	var hookedQuery string

	db, err := duosql.Open(duosql.DialectSQLite, "file::memory:?cache=shared",
		duosql.WithMaxOpenConns(5),
		duosql.WithMaxIdleConns(2),
		duosql.WithConnMaxLifetime(10*time.Minute),
		duosql.WithConnMaxIdleTime(5*time.Minute),
		duosql.WithHook(func(ctx context.Context, query string, args []any, duration time.Duration, err error) {
			hookFired = true
			hookedQuery = query
		}),
	)
	if err != nil {
		t.Fatalf("open with options failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	_ = db.Ping(ctx)
	if db.SQLDB() == nil {
		t.Fatalf("expected SQLDB() to be non nil")
	}

	// Test predicates: Neq, Lt, Lte, NotIn, Like, IsNull, IsNotNull, Or, Not, Raw
	dummyDB := duosql.Wrap(&sql.DB{}, duosql.NewSQLiteDialect())
	bc := duosql.NewBuildContext(dummyDB.Dialect())

	preds := []duosql.Predicate{
		duosql.Neq("a", 1),
		duosql.Lt("b", 2),
		duosql.Lte("c", 3),
		duosql.NotIn("d", 4, 5),
		duosql.Like("e", "%test%"),
		duosql.IsNull("f"),
		duosql.IsNotNull("g"),
		duosql.Or(duosql.Eq("h", 1), duosql.Eq("i", 2)),
		duosql.Not(duosql.Eq("j", 3)),
		duosql.Raw("k = ?", 10),
		duosql.Col("l").Neq(1),
		duosql.Col("m").Gt(2),
		duosql.Col("n").Gte(3),
		duosql.Col("o").Lt(4),
		duosql.Col("p").Lte(5),
		duosql.Col("q").In(6, 7),
		duosql.Col("r").NotIn(8, 9),
		duosql.Col("s").Like("%foo%"),
		duosql.Col("t").IsNull(),
		duosql.Col("u").IsNotNull(),
		duosql.Col("v").Between(1, 10),
	}

	for _, p := range preds {
		sqlPart, _, err := p.ToSQL(bc)
		if err != nil || sqlPart == "" {
			t.Fatalf("predicate compilation failed for %v: err=%v", p, err)
		}
	}

	// ColumnExpr ordering
	_ = duosql.Col("x").Asc().ToOrderBySQL(bc)
	_ = duosql.Col("y").Desc().ToOrderBySQL(bc)

	// Execute a dummy query to verify hook firing
	_, _ = db.ExecContext(ctx, "SELECT 1")
	if !hookFired {
		t.Errorf("expected hook to fire on query execution")
	}
	_ = hookedQuery
}

type Post struct {
	ID        int64      `duo:"id,pk,auto"`
	Title     string     `duo:"title"`
	Likes     int64      `duo:"likes"`
	DeletedAt *time.Time `duo:"deleted_at,soft_delete"`
}

func TestSoftDeletesAndIncDec(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()

	err := db.Schema().CreateTable("posts", func(tb *duosql.TableBuilder) {
		tb.ID()
		tb.String("title", 200).NotNull()
		tb.BigInt("likes").Default(0)
		tb.Timestamp("deleted_at").Nullable()
	}).IfNotExists().Exec(ctx)
	if err != nil {
		t.Fatalf("failed to create posts table: %v", err)
	}

	p, err := duosql.Insert[Post](db).
		Values(&Post{Title: "Intro to DuoSQL", Likes: 10}).
		Returning("*").
		One(ctx)
	if err != nil {
		t.Fatalf("failed to insert post: %v", err)
	}

	// Atomic increment and decrement
	_, err = duosql.Update[Post](db).Where(duosql.Eq("id", p.ID)).Inc("likes", 5).Exec(ctx)
	if err != nil {
		t.Fatalf("inc likes failed: %v", err)
	}
	_, err = duosql.Update[Post](db).Where(duosql.Eq("id", p.ID)).Dec("likes", 2).Exec(ctx)
	if err != nil {
		t.Fatalf("dec likes failed: %v", err)
	}

	upP, _ := duosql.Select[Post](db).Where(duosql.Eq("id", p.ID)).One(ctx)
	if upP.Likes != 13 {
		t.Errorf("expected 13 likes after inc/dec, got %d", upP.Likes)
	}

	// Soft delete execution
	rowsAff, err := duosql.Delete[Post](db).Where(duosql.Eq("id", p.ID)).Exec(ctx)
	if err != nil {
		t.Fatalf("soft delete failed: %v", err)
	}
	if rowsAff != 1 {
		t.Errorf("expected 1 row soft deleted, got %d", rowsAff)
	}

	// Regular select must exclude soft-deleted post
	_, err = duosql.Select[Post](db).Where(duosql.Eq("id", p.ID)).One(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows for soft-deleted post, got %v", err)
	}

	// WithTrashed retrieves soft-deleted post
	trashed, err := duosql.Select[Post](db).WithTrashed().Where(duosql.Eq("id", p.ID)).One(ctx)
	if err != nil || trashed.ID != p.ID {
		t.Fatalf("expected WithTrashed to return post, err: %v", err)
	}

	// OnlyTrashed retrieves only soft-deleted records
	onlyTrashedList, err := duosql.Select[Post](db).OnlyTrashed().All(ctx)
	if err != nil || len(onlyTrashedList) != 1 {
		t.Fatalf("expected 1 record in OnlyTrashed, got %d (err: %v)", len(onlyTrashedList), err)
	}

	// ForceDelete permanently deletes from database
	_, err = duosql.Delete[Post](db).ForceDelete().Where(duosql.Eq("id", p.ID)).Exec(ctx)
	if err != nil {
		t.Fatalf("force delete failed: %v", err)
	}
	_, err = duosql.Select[Post](db).WithTrashed().Where(duosql.Eq("id", p.ID)).One(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows after force delete, got %v", err)
	}
}

func TestAggregationsAndPluck(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	for i := 1; i <= 6; i++ {
		_, err := duosql.Insert[User](db).Values(&User{
			Username:  fmt.Sprintf("agg_user_%d", i),
			Email:     fmt.Sprintf("agg_%d@domain.com", i),
			Age:       i * 10,
			Active:    true,
			CreatedAt: now,
		}).Exec(ctx)
		if err != nil {
			t.Fatalf("failed inserting agg user %d: %v", i, err)
		}
	}

	sumAge, err := duosql.Select[User](db).Where(duosql.Col("username").Like("agg_user_%")).Sum(ctx, "age")
	if err != nil || sumAge != 210 {
		t.Errorf("expected sum 210, got %v (err: %v)", sumAge, err)
	}

	avgAge, err := duosql.Select[User](db).Where(duosql.Col("username").Like("agg_user_%")).Avg(ctx, "age")
	if err != nil || avgAge != 35 {
		t.Errorf("expected avg 35, got %v (err: %v)", avgAge, err)
	}

	minAge, err := duosql.Select[User](db).Where(duosql.Col("username").Like("agg_user_%")).Min(ctx, "age")
	if err != nil || fmt.Sprintf("%v", minAge) != "10" {
		t.Errorf("expected min 10, got %v (err: %v)", minAge, err)
	}

	maxAge, err := duosql.Select[User](db).Where(duosql.Col("username").Like("agg_user_%")).Max(ctx, "age")
	if err != nil || fmt.Sprintf("%v", maxAge) != "60" {
		t.Errorf("expected max 60, got %v (err: %v)", maxAge, err)
	}

	names, err := duosql.Pluck[string](duosql.Select[User](db).Where(duosql.Col("username").Like("agg_user_%")).Asc("age"), ctx, "username")
	if err != nil || len(names) != 6 {
		t.Fatalf("expected 6 plucked usernames, got %d (err: %v)", len(names), err)
	}
	if names[0] != "agg_user_1" {
		t.Errorf("expected first plucked username agg_user_1, got %s", names[0])
	}
}

func TestInsertBatchesAndPagination(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()

	err := db.Schema().AlterTable("users", func(ab *duosql.AlterTableBuilder) {
		ab.AddColumn("tag", duosql.TypeString).Nullable()
	}).Exec(ctx)
	if err != nil {
		t.Fatalf("alter table add column failed: %v", err)
	}

	now := time.Now()
	var newUsers []*User
	for i := 1; i <= 4; i++ {
		newUsers = append(newUsers, &User{
			Username:  fmt.Sprintf("batch_user_%d", i),
			Email:     fmt.Sprintf("batch_%d@domain.com", i),
			Age:       i * 10,
			Active:    true,
			CreatedAt: now,
		})
	}

	err = duosql.Insert[User](db).Values(newUsers...).InsertInBatches(ctx, 2)
	if err != nil {
		t.Fatalf("insert in batches failed: %v", err)
	}

	pageUsers, err := duosql.Select[User](db).
		Where(duosql.Col("username").Like("batch_user_%")).
		Asc("age").
		Paginate(2, 2).
		All(ctx)
	if err != nil || len(pageUsers) != 2 {
		t.Fatalf("expected 2 users in page 2, got %d (err: %v)", len(pageUsers), err)
	}
	if pageUsers[0].Username != "batch_user_3" {
		t.Errorf("expected batch_user_3 on page 2, got %s", pageUsers[0].Username)
	}
}

func TestSubqueriesAndJSONExtraction(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	_, err := duosql.Insert[User](db).Values(
		&User{Username: "jadmin", Email: "admin@corp.com", Age: 35, Active: true, Profile: &UserProfile{Role: "superuser"}, CreatedAt: now},
		&User{Username: "jguest", Email: "guest@corp.com", Age: 25, Active: true, Profile: &UserProfile{Role: "visitor"}, CreatedAt: now},
	).Exec(ctx)
	if err != nil {
		t.Fatalf("insert json users failed: %v", err)
	}

	// Test InSubquery and ExistsSubquery
	subquery := duosql.Select[User](db, "username").Where(duosql.Gte("age", 30))

	matched, err := duosql.Select[User](db).
		Where(duosql.InSubquery("username", subquery)).
		All(ctx)
	if err != nil {
		t.Fatalf("InSubquery failed: %v", err)
	}
	if len(matched) == 0 {
		t.Errorf("expected at least 1 match from InSubquery")
	}

	existsMatches, err := duosql.Select[User](db).
		Where(duosql.ExistsSubquery(subquery)).
		All(ctx)
	if err != nil || len(existsMatches) == 0 {
		t.Fatalf("ExistsSubquery failed: %v", err)
	}

	// Test NotInSubquery and NotExistsSubquery compilation
	_ = duosql.NotInSubquery("username", subquery)
	_ = duosql.NotExistsSubquery(subquery)

	// Test JSONExtract
	jsonMatches, err := duosql.Select[User](db).
		Where(duosql.JSONExtract("profile", "role").Eq("superuser")).
		All(ctx)
	if err != nil {
		t.Fatalf("JSONExtract query failed: %v", err)
	}
	if len(jsonMatches) != 1 || jsonMatches[0].Username != "jadmin" {
		t.Errorf("expected jadmin to match JSONExtract superuser, got %+v", jsonMatches)
	}

	// Test JSONExtract Neq & Like
	_ = duosql.JSONExtract("profile", "role").Neq("visitor")
	_ = duosql.JSONExtract("profile", "role").Like("%user%")
}

type PgUser struct {
	ID        int64        `duo:"id,pk,auto"`
	Username  string       `duo:"username"`
	Email     string       `duo:"email"`
	Age       int          `duo:"age"`
	Active    bool         `duo:"active"`
	Profile   *UserProfile `duo:"profile,json"`
	CreatedAt time.Time    `duo:"created_at"`
}

func setupLivePostgres(t *testing.T) (*duosql.DB, context.Context) {
	t.Helper()
	pgDSN := "postgres://thruqe:postgres@localhost:5432/duosql_test?sslmode=disable"
	db, err := duosql.Open(duosql.DialectPostgres, pgDSN)
	if err != nil {
		t.Skipf("skipping live postgres test (connection unavailable): %v", err)
	}

	ctx := context.Background()
	if err := db.Ping(ctx); err != nil {
		db.Close()
		t.Skipf("skipping live postgres test (ping failed): %v", err)
	}

	_ = db.Schema().DropTable("pg_users").Cascade().IfExists().Exec(ctx)

	err = db.Schema().CreateTable("pg_users", func(tb *duosql.TableBuilder) {
		tb.ID()
		tb.String("username", 100).NotNull().Unique()
		tb.String("email", 255).NotNull()
		tb.Int("age").Default(18)
		tb.Bool("active").Default(true)
		tb.JSON("profile").Nullable()
		tb.Timestamp("created_at").NotNull()
	}).Exec(ctx)
	if err != nil {
		db.Close()
		t.Fatalf("postgres create table failed: %v", err)
	}

	return db, ctx
}

func TestLivePostgresCRUD(t *testing.T) {
	db, ctx := setupLivePostgres(t)
	defer db.Close()
	defer func() { _ = db.Schema().DropTable("pg_users").Cascade().Exec(ctx) }()

	exists, err := db.Schema().HasTable(ctx, "pg_users")
	if err != nil || !exists {
		t.Fatalf("expected HasTable pg_users to be true on Postgres: exists=%v, err=%v", exists, err)
	}

	now := time.Now().Truncate(time.Microsecond)
	newUser := &PgUser{
		Username:  "pg_guru",
		Email:     "pg@domain.com",
		Age:       32,
		Active:    true,
		Profile:   &UserProfile{Theme: "cyber", Role: "architect"},
		CreatedAt: now,
	}

	inserted, err := duosql.Insert[PgUser](db).
		Into("pg_users").
		Values(newUser).
		Returning("*").
		One(ctx)
	if err != nil {
		t.Fatalf("postgres insert with returning failed: %v", err)
	}
	if inserted.ID == 0 || inserted.Username != "pg_guru" {
		t.Errorf("unexpected inserted pg user: %+v", inserted)
	}

	var readCount int
	for u, err := range duosql.Select[PgUser](db).From("pg_users").Iter(ctx) {
		if err != nil {
			t.Fatalf("postgres stream iterator error: %v", err)
		}
		if u.Username == "pg_guru" {
			readCount++
		}
	}
	if readCount != 1 {
		t.Errorf("expected 1 streamed user from postgres, got %d", readCount)
	}
}

func TestLivePostgresUpsertAndTx(t *testing.T) {
	db, ctx := setupLivePostgres(t)
	defer db.Close()
	defer func() { _ = db.Schema().DropTable("pg_users").Cascade().Exec(ctx) }()

	now := time.Now().Truncate(time.Microsecond)
	seedUser := &PgUser{
		Username:  "pg_upsert_user",
		Email:     "initial@domain.com",
		Age:       30,
		Active:    true,
		CreatedAt: now,
	}
	_, err := duosql.Insert[PgUser](db).Into("pg_users").Values(seedUser).Exec(ctx)
	if err != nil {
		t.Fatalf("seed insert failed: %v", err)
	}

	seedUser.Email = "updated@domain.com"
	_, err = duosql.Insert[PgUser](db).
		Into("pg_users").
		Values(seedUser).
		OnConflictDoUpdate([]string{"username"}, []string{"email"}).
		Exec(ctx)
	if err != nil {
		t.Fatalf("postgres upsert failed: %v", err)
	}

	updated, _ := duosql.Select[PgUser](db).From("pg_users").Where(duosql.Eq("username", "pg_upsert_user")).One(ctx)
	if updated.Email != "updated@domain.com" {
		t.Errorf("expected updated email on postgres, got %s", updated.Email)
	}

	err = db.Transaction(ctx, func(tx *duosql.Tx) error {
		_, err := duosql.Insert[PgUser](tx).Into("pg_users").Values(&PgUser{
			Username:  "tx_pg_user",
			Email:     "txpg@domain.com",
			Age:       24,
			Active:    true,
			CreatedAt: now,
		}).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatalf("postgres transaction failed: %v", err)
	}
}

func TestLivePostgresProtoAndIndexing(t *testing.T) {
	pgDSN := "postgres://thruqe:postgres@localhost:5432/duosql_test?sslmode=disable"
	db, err := duosql.Open(duosql.DialectPostgres, pgDSN)
	if err != nil {
		t.Skipf("skipping live postgres proto/index test: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Ping(ctx); err != nil {
		t.Skipf("skipping live postgres test (ping failed): %v", err)
	}

	_ = db.Schema().DropTable("indexed_catalog_items").Cascade().IfExists().Exec(ctx)
	_ = db.Schema().DropTable("proto_users").Cascade().IfExists().Exec(ctx)

	// 1. Create table from model with indexes on Postgres
	err = duosql.CreateTableFromModel[IndexedCatalogItem](ctx, db.Schema(), true)
	if err != nil {
		t.Fatalf("postgres CreateTableFromModel failed: %v", err)
	}
	defer func() { _ = db.Schema().DropTable("indexed_catalog_items").Cascade().Exec(ctx) }()

	hasSlugIdx, err := db.Schema().HasIndex(ctx, "indexed_catalog_items", "uniq_indexed_catalog_items_slug")
	if err != nil || !hasSlugIdx {
		t.Errorf("postgres: expected uniq slug index: %v", err)
	}

	// 2. Proto columns on Postgres
	err = duosql.CreateTableFromModel[ProtoUser](ctx, db.Schema(), true)
	if err != nil {
		t.Fatalf("postgres CreateTableFromModel ProtoUser failed: %v", err)
	}
	defer func() { _ = db.Schema().DropTable("proto_users").Cascade().Exec(ctx) }()

	now := time.Now().UTC().Truncate(time.Second)
	pUser := &ProtoUser{
		Name:    "PG Proto Master",
		Payload: wrapperspb.String("pg-proto-content"),
		Stamp:   timestamppb.New(now),
	}
	_, err = duosql.Insert[ProtoUser](db).Values(pUser).Exec(ctx)
	if err != nil {
		t.Fatalf("postgres insert proto user failed: %v", err)
	}

	fetched, err := duosql.Select[ProtoUser](db).Where(duosql.Eq("name", "PG Proto Master")).One(ctx)
	if err != nil {
		t.Fatalf("postgres fetch proto user failed: %v", err)
	}
	if fetched.Payload == nil || fetched.Payload.Value != "pg-proto-content" {
		t.Fatalf("postgres unexpected fetched proto payload: %+v", fetched.Payload)
	}
}

func TestAutomaticTimestampsAndUpsertAll(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	type Article struct {
		ID        int64     `db:"id,pk,auto"`
		Slug      string    `db:"slug"`
		Title     string    `db:"title"`
		CreatedAt time.Time `db:"created_at"`
		UpdatedAt time.Time `db:"updated_at"`
	}

	err := db.Schema().CreateTable("articles", func(table *duosql.TableBuilder) {
		table.BigInt("id").AutoIncrement()
		table.String("slug").NotNull().Unique()
		table.String("title").NotNull()
		table.Timestamps()
	}).IfNotExists().Exec(ctx)
	if err != nil {
		t.Fatalf("failed creating articles table: %v", err)
	}

	hasCol, err := db.Schema().HasColumn(ctx, "articles", "updated_at")
	if err != nil || !hasCol {
		t.Fatalf("expected HasColumn updated_at to be true: hasCol=%v, err=%v", hasCol, err)
	}

	// 1. Insert without setting timestamps - should be auto-populated
	art := &Article{
		Slug:  "go-127",
		Title: "Go 1.27 Modernization",
	}

	_, err = duosql.Insert[Article](db).
		Into("articles").
		Values(art).
		Exec(ctx)
	if err != nil {
		t.Fatalf("insert article failed: %v", err)
	}

	saved, err := duosql.Select[Article](db).From("articles").Where(duosql.Eq("slug", "go-127")).One(ctx)
	if err != nil {
		t.Fatalf("fetch article failed: %v", err)
	}
	if saved.CreatedAt.IsZero() || saved.UpdatedAt.IsZero() {
		t.Fatalf("expected non-zero auto timestamps, got created=%v, updated=%v", saved.CreatedAt, saved.UpdatedAt)
	}

	// 2. OnConflictDoUpdateAll
	artUpdate := &Article{
		Slug:  "go-127",
		Title: "Go 1.27 Modernization (Updated)",
	}
	_, err = duosql.Insert[Article](db).
		Into("articles").
		Values(artUpdate).
		OnConflictDoUpdateAll("slug").
		Exec(ctx)
	if err != nil {
		t.Fatalf("OnConflictDoUpdateAll failed: %v", err)
	}

	updatedArt, err := duosql.Select[Article](db).From("articles").Where(duosql.Eq("slug", "go-127")).One(ctx)
	if err != nil {
		t.Fatalf("fetch updated article failed: %v", err)
	}
	if updatedArt.Title != "Go 1.27 Modernization (Updated)" {
		t.Errorf("expected updated title, got %s", updatedArt.Title)
	}

	// 3. OnConflictDoUpdateRaw and RawOrderBy
	artRaw := &Article{
		Slug:  "go-127",
		Title: "Go 1.27 Modernization (Raw Update)",
	}
	_, err = duosql.Insert[Article](db).
		Into("articles").
		Values(artRaw).
		OnConflictDoUpdateRaw([]string{"slug"}, []string{"title = 'Go 1.27 Modernization (Raw Overwrite)'"}).
		Exec(ctx)
	if err != nil {
		t.Fatalf("OnConflictDoUpdateRaw failed: %v", err)
	}

	rawUpdated, err := duosql.Select[Article](db).
		From("articles").
		OrderBy(duosql.RawOrderBy("slug ASC")).
		One(ctx)
	if err != nil {
		t.Fatalf("fetch raw updated article failed: %v", err)
	}
	if rawUpdated.Title != "Go 1.27 Modernization (Raw Overwrite)" {
		t.Errorf("expected raw updated title, got %s", rawUpdated.Title)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || stringSearch(s, sub))
}

func stringSearch(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

type ValidatedUser struct {
	ID    int64  `duo:"id,pk,auto"`
	Name  string `duo:"name" validate:"required,min=3,max=50"`
	Email string `duo:"email" validate:"required,email"`
	Role  string `duo:"role" validate:"in=admin|user|guest"`
	UUID  string `duo:"uuid" validate:"uuid"`
	Age   int    `duo:"age" validate:"min=18,max=120"`
}

func (ValidatedUser) TableName() string { return "validated_users" }

func TestBuiltinValidation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	invalid := &ValidatedUser{
		Name:  "al",
		Email: "not-an-email",
		Role:  "superuser",
		UUID:  "123-bad",
		Age:   15,
	}
	err := duosql.Validate(invalid)
	if err == nil {
		t.Fatalf("expected validation failure for invalid user")
	}
	var ve duosql.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors type, got %T", err)
	}
	fieldErrs := ve.FieldErrors()
	if fieldErrs["name"] == "" || fieldErrs["email"] == "" || fieldErrs["role"] == "" || fieldErrs["age"] == "" {
		t.Errorf("missing expected field error keys: %+v", fieldErrs)
	}

	_, err = duosql.Insert[ValidatedUser](db).Values(invalid).Exec(ctx)
	if err == nil {
		t.Fatalf("expected Insert to reject invalid model before execution")
	}

	_ = db.Schema().CreateTableIfNotExists("validated_users", func(tb *duosql.TableBuilder) {
		tb.ID()
		tb.String("name")
		tb.String("email")
		tb.String("role")
		tb.String("uuid")
		tb.Int("age")
	}).Exec(ctx)

	valid := &ValidatedUser{
		Name:  "Alice",
		Email: "alice@example.com",
		Role:  "admin",
		UUID:  "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		Age:   28,
	}
	_, err = duosql.Insert[ValidatedUser](db).Values(valid).Exec(ctx)
	if err != nil {
		t.Fatalf("insert valid user failed: %v", err)
	}

	// 4. SkipValidation permits execution of invalid entity
	_, err = duosql.Insert[ValidatedUser](db).Values(invalid).SkipValidation().Exec(ctx)
	if err != nil {
		t.Fatalf("expected SkipValidation to permit insert: %v", err)
	}
}

type ProtoUser struct {
	ID      int64                   `duo:"id,pk,auto"`
	Name    string                  `duo:"name"`
	Payload *wrapperspb.StringValue `duo:"payload,proto"`
	Stamp   *timestamppb.Timestamp  `duo:"stamp,proto"`
}

func (ProtoUser) TableName() string { return "proto_users" }

func TestProtobufSupport(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	err := db.Schema().CreateTable("proto_users", func(tb *duosql.TableBuilder) {
		tb.ID()
		tb.String("name")
		tb.Proto("payload")
		tb.Proto("stamp")
	}).Exec(ctx)
	if err != nil {
		t.Fatalf("create proto_users table failed: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	pUser := &ProtoUser{
		Name:    "Protobuf Hero",
		Payload: wrapperspb.String("duosql-proto-payload-test"),
		Stamp:   timestamppb.New(now),
	}

	_, err = duosql.Insert[ProtoUser](db).Values(pUser).Exec(ctx)
	if err != nil {
		t.Fatalf("insert proto user failed: %v", err)
	}

	fetched, err := duosql.Select[ProtoUser](db).Where(duosql.Eq("name", "Protobuf Hero")).One(ctx)
	if err != nil {
		t.Fatalf("fetch proto user failed: %v", err)
	}
	if fetched.Payload == nil || fetched.Payload.Value != "duosql-proto-payload-test" {
		t.Fatalf("unexpected fetched proto payload: %+v", fetched.Payload)
	}
	if fetched.Stamp == nil || fetched.Stamp.AsTime().Unix() != now.Unix() {
		t.Fatalf("unexpected fetched proto stamp: %+v", fetched.Stamp)
	}

	newPayload := wrapperspb.String("duosql-proto-updated")
	_, err = duosql.Update[ProtoUser](db).
		Set("payload", newPayload).
		Where(duosql.Eq("id", fetched.ID)).
		Exec(ctx)
	if err != nil {
		t.Fatalf("update proto column failed: %v", err)
	}

	updated, err := duosql.Select[ProtoUser](db).Where(duosql.Eq("id", fetched.ID)).One(ctx)
	if err != nil {
		t.Fatalf("fetch updated proto user failed: %v", err)
	}
	if updated.Payload.Value != "duosql-proto-updated" {
		t.Errorf("expected updated proto payload 'duosql-proto-updated', got %q", updated.Payload.Value)
	}
}

type IndexedCatalogItem struct {
	ID       int64  `duo:"id,pk,auto"`
	Slug     string `duo:"slug,unique"`
	Category string `duo:"category,index"`
	Status   string `duo:"status,index:idx_item_status"`
}

func (IndexedCatalogItem) TableName() string { return "indexed_catalog_items" }

func TestIndexingAndSchemaFromModel(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	err := duosql.CreateTableFromModel[IndexedCatalogItem](ctx, db.Schema(), true)
	if err != nil {
		t.Fatalf("CreateTableFromModel failed: %v", err)
	}

	hasTable, err := db.Schema().HasTable(ctx, "indexed_catalog_items")
	if err != nil || !hasTable {
		t.Fatalf("expected indexed_catalog_items table to exist: %v", err)
	}

	hasSlugIdx, err := db.Schema().HasIndex(ctx, "indexed_catalog_items", "uniq_indexed_catalog_items_slug")
	if err != nil || !hasSlugIdx {
		t.Errorf("expected uniq slug index to exist: has=%v, err=%v", hasSlugIdx, err)
	}

	hasStatusIdx, err := db.Schema().HasIndex(ctx, "indexed_catalog_items", "idx_item_status")
	if err != nil || !hasStatusIdx {
		t.Errorf("expected idx_item_status to exist: has=%v, err=%v", hasStatusIdx, err)
	}

	err = db.Schema().CreateIndex("idx_catalog_partial").
		On("indexed_catalog_items", "category").
		Where("status = 'active'").
		Exec(ctx)
	if err != nil {
		t.Fatalf("create partial index failed: %v", err)
	}

	hasPartial, err := db.Schema().HasIndex(ctx, "indexed_catalog_items", "idx_catalog_partial")
	if err != nil || !hasPartial {
		t.Errorf("expected partial index to exist: %v", err)
	}

	err = db.Schema().DropIndex("idx_catalog_partial").IfExists().Exec(ctx)
	if err != nil {
		t.Fatalf("drop index failed: %v", err)
	}
	hasAfterDrop, _ := db.Schema().HasIndex(ctx, "indexed_catalog_items", "idx_catalog_partial")
	if hasAfterDrop {
		t.Errorf("expected partial index to be removed after drop")
	}
}
