package db_test

import (
	"context"
	"testing"

	"github.com/netfishx/gabon-go/internal/testdb"
)

// 钉死 schema 不变量：已提交及之后状态的限时任务领取必须带 1–9 张证明图；
// claimed/expired（未提交）允许为空。见 docs/schema.md 任务域。
func TestTaskClaimProofImagesBounds(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	// FK 前置：一个客户 + 一个限时任务定义
	var customerID, taskID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('pub_constraint', 'constraint_user', 'x', 'CONSTRNT')
		 RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO claim_tasks (name, reward) VALUES ('t', 100) RETURNING id`,
	).Scan(&taskID); err != nil {
		t.Fatalf("insert claim task: %v", err)
	}

	tests := []struct {
		name    string
		status  string
		images  string
		wantErr bool
	}{
		{"claimed_empty_ok", "claimed", "{}", false},
		{"expired_empty_ok", "expired", "{}", false},
		{"submitted_empty_rejected", "submitted", "{}", true},
		{"approved_empty_rejected", "approved", "{}", true},
		{"rewarded_empty_rejected", "rewarded", "{}", true},
		{"rejected_empty_rejected", "rejected", "{}", true},
		{"submitted_one_ok", "submitted", `{"proofs/1/a.jpg"}`, false},
		{"submitted_nine_ok", "submitted", `{"a","b","c","d","e","f","g","h","i"}`, false},
		{"submitted_ten_rejected", "submitted", `{"a","b","c","d","e","f","g","h","i","j"}`, true},
		{"claimed_ten_rejected", "claimed", `{"a","b","c","d","e","f","g","h","i","j"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 唯一约束 (customer_id, task_id)：无论上个用例结果如何，先清场
			if _, err := pool.Exec(
				ctx,
				`DELETE FROM task_claims WHERE customer_id = $1 AND task_id = $2`, customerID, taskID,
			); err != nil {
				t.Fatalf("cleanup claim: %v", err)
			}
			_, err := pool.Exec(
				ctx,
				`INSERT INTO task_claims (customer_id, task_id, status, proof_images, reward_base)
				 VALUES ($1, $2, $3, $4, 100)`,
				customerID, taskID, tt.status, tt.images,
			)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("insert succeeded, want CHECK violation")
				}
				return
			}
			if err != nil {
				t.Fatalf("insert failed: %v", err)
			}
		})
	}
}
