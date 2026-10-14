package db_test

import (
	"context"
	"fmt"
	"testing"

	"goblast/internal/testutil"
)

func TestInspectRealCatalog(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	// 1. Cek email duplikat di users dan integritas data campaign_numbers
	t.Run("check duplicate lower email and campaign_numbers integrity", func(t *testing.T) {
		rows, err := pool.Query(ctx, `
			SELECT LOWER(email), COUNT(*)
			FROM users
			WHERE email IS NOT NULL
			GROUP BY LOWER(email)
			HAVING COUNT(*) > 1
		`)
		if err != nil {
			t.Fatalf("query duplicate email error: %v", err)
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var email string
			var n int
			_ = rows.Scan(&email, &n)
			t.Logf("DUPLICATE EMAIL FOUND: %s (%d times)", email, n)
			count++
		}
		t.Logf("Total duplicate lower(email) groups: %d", count)

		var v1, v2, v3 int
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE attempts < 0").Scan(&v1)
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE status = 'dikirim' AND (claimed_account_id IS NULL OR claimed_at IS NULL)").Scan(&v2)
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM campaign_numbers WHERE status = 'tersedia' AND claimed_account_id IS NOT NULL").Scan(&v3)
		t.Logf("Violations in campaign_numbers: attempts<0: %d, dikirim_unclaimed: %d, tersedia_claimed: %d", v1, v2, v3)
	})

	// 2. Query Tables
	t.Run("dump tables and columns", func(t *testing.T) {
		rows, err := pool.Query(ctx, `
			SELECT table_name
			FROM information_schema.tables
			WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
			ORDER BY table_name
		`)
		if err != nil {
			t.Fatalf("query tables error: %v", err)
		}
		defer rows.Close()

		var tables []string
		for rows.Next() {
			var tbl string
			_ = rows.Scan(&tbl)
			tables = append(tables, tbl)
		}

		for _, tbl := range tables {
			t.Logf("=== TABLE: %s ===", tbl)
			colRows, err := pool.Query(ctx, `
				SELECT column_name, data_type, is_nullable, column_default
				FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1
				ORDER BY ordinal_position
			`, tbl)
			if err != nil {
				t.Fatalf("query cols for %s error: %v", tbl, err)
			}
			for colRows.Next() {
				var col, dt, nullable string
				var dflt *string
				_ = colRows.Scan(&col, &dt, &nullable, &dflt)
				d := "<null>"
				if dflt != nil {
					d = *dflt
				}
				t.Logf("  COL: %-25s %-20s NULLABLE=%-4s DEFAULT=%s", col, dt, nullable, d)
			}
			colRows.Close()

			// Constraints
			conRows, err := pool.Query(ctx, `
				SELECT conname, contype, pg_get_constraintdef(c.oid)
				FROM pg_constraint c
				JOIN pg_namespace n ON n.oid = c.connamespace
				JOIN pg_class cl ON cl.oid = c.conrelid
				WHERE n.nspname = 'public' AND cl.relname = $1
				ORDER BY conname
			`, tbl)
			if err != nil {
				t.Fatalf("query constraints for %s error: %v", tbl, err)
			}
			for conRows.Next() {
				var name, ctype, def string
				_ = conRows.Scan(&name, &ctype, &def)
				t.Logf("  CON [%s]: %-30s DEF=%s", ctype, name, def)
			}
			conRows.Close()

			// Indexes
			idxRows, err := pool.Query(ctx, `
				SELECT indexname, indexdef
				FROM pg_indexes
				WHERE schemaname = 'public' AND tablename = $1
				ORDER BY indexname
			`, tbl)
			if err != nil {
				t.Fatalf("query indexes for %s error: %v", tbl, err)
			}
			for idxRows.Next() {
				var iname, idef string
				_ = idxRows.Scan(&iname, &idef)
				t.Logf("  IDX: %-30s DEF=%s", iname, idef)
			}
			idxRows.Close()

			// Triggers
			trigRows, err := pool.Query(ctx, `
				SELECT tgname, pg_get_triggerdef(t.oid)
				FROM pg_trigger t
				JOIN pg_class cl ON cl.oid = t.tgrelid
				JOIN pg_namespace n ON n.oid = cl.relnamespace
				WHERE n.nspname = 'public' AND cl.relname = $1 AND NOT t.tgisinternal
				ORDER BY tgname
			`, tbl)
			if err != nil {
				t.Fatalf("query triggers for %s error: %v", tbl, err)
			}
			for trigRows.Next() {
				var tgname, tgdef string
				_ = trigRows.Scan(&tgname, &tgdef)
				t.Logf("  TRG: %-30s DEF=%s", tgname, tgdef)
			}
			trigRows.Close()
		}
	})

	// 3. Foreign Keys audit & check supporting indexes
	t.Run("audit foreign keys and indexes", func(t *testing.T) {
		query := `
			SELECT
				tc.table_name AS child_table,
				kcu.column_name AS child_column,
				ccu.table_name AS parent_table,
				ccu.column_name AS parent_column,
				rc.update_rule,
				rc.delete_rule,
				tc.constraint_name
			FROM information_schema.table_constraints AS tc
			JOIN information_schema.key_column_usage AS kcu
				ON tc.constraint_name = kcu.constraint_name
				AND tc.table_schema = kcu.table_schema
			JOIN information_schema.constraint_column_usage AS ccu
				ON ccu.constraint_name = tc.constraint_name
				AND ccu.table_schema = tc.table_schema
			JOIN information_schema.referential_constraints AS rc
				ON rc.constraint_name = tc.constraint_name
			WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = 'public'
			ORDER BY tc.table_name, kcu.column_name;
		`
		rows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatalf("query fk error: %v", err)
		}
		defer rows.Close()

		type FKInfo struct {
			childTable   string
			childColumn  string
			parentTable  string
			parentColumn string
			updateRule   string
			deleteRule   string
			conName      string
		}

		var fks []FKInfo
		for rows.Next() {
			var fk FKInfo
			_ = rows.Scan(&fk.childTable, &fk.childColumn, &fk.parentTable, &fk.parentColumn, &fk.updateRule, &fk.deleteRule, &fk.conName)
			fks = append(fks, fk)
		}

		t.Logf("=== FOREIGN KEYS LIST (%d total) ===", len(fks))
		for _, fk := range fks {
			// Cek apakah kolom child punya index pendukung di mana childColumn adalah leading column
			idxQuery := `
				SELECT i.relname AS index_name, pg_get_indexdef(i.oid) AS index_def
				FROM pg_index x
				JOIN pg_class c ON c.oid = x.indrelid
				JOIN pg_class i ON i.oid = x.indexrelid
				JOIN pg_namespace n ON n.oid = c.relnamespace
				WHERE n.nspname = 'public'
				  AND c.relname = $1
				  AND (
					SELECT attname FROM pg_attribute
					WHERE attrelid = c.oid AND attnum = x.indkey[0]
				  ) = $2
			`
			var idxName, idxDef string
			err := pool.QueryRow(ctx, idxQuery, fk.childTable, fk.childColumn).Scan(&idxName, &idxDef)
			hasIndex := err == nil
			status := "NO_INDEX"
			if hasIndex {
				status = fmt.Sprintf("INDEXED(%s)", idxName)
			}
			t.Logf("FK: %s.%s -> %s.%s [ON DELETE %s] [%s]",
				fk.childTable, fk.childColumn, fk.parentTable, fk.parentColumn, fk.deleteRule, status)
		}
	})

	// 4. Baris yatim check
	t.Run("orphan rows check", func(t *testing.T) {
		query := `
			SELECT
				tc.table_name AS child_table,
				kcu.column_name AS child_column,
				ccu.table_name AS parent_table,
				ccu.column_name AS parent_column
			FROM information_schema.table_constraints AS tc
			JOIN information_schema.key_column_usage AS kcu
				ON tc.constraint_name = kcu.constraint_name
				AND tc.table_schema = kcu.table_schema
			JOIN information_schema.constraint_column_usage AS ccu
				ON ccu.constraint_name = tc.constraint_name
				AND ccu.table_schema = tc.table_schema
			WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = 'public'
			ORDER BY tc.table_name, kcu.column_name;
		`
		rows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatalf("query fk error: %v", err)
		}
		defer rows.Close()

		for rows.Next() {
			var cTable, cCol, pTable, pCol string
			_ = rows.Scan(&cTable, &cCol, &pTable, &pCol)
			orphanQ := fmt.Sprintf(`
				SELECT COUNT(*)
				FROM %s c
				LEFT JOIN %s p ON p.%s = c.%s
				WHERE c.%s IS NOT NULL AND p.%s IS NULL
			`, cTable, pTable, pCol, cCol, cCol, pCol)

			var orphanCount int
			err := pool.QueryRow(ctx, orphanQ).Scan(&orphanCount)
			if err != nil {
				t.Fatalf("query orphan for %s.%s -> %s.%s error: %v", cTable, cCol, pTable, pCol, err)
			}
			if orphanCount > 0 {
				t.Errorf("ORPHAN ROWS FOUND: %s.%s -> %s.%s has %d orphans!", cTable, cCol, pTable, pCol, orphanCount)
			} else {
				t.Logf("Orphan check passed (0): %s.%s -> %s.%s", cTable, cCol, pTable, pCol)
			}
		}
	})
}
