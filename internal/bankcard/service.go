// Package bankcard 银行卡管理（提现收款目标，#67 消费）。
package bankcard

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

// Service 银行卡域服务。
type Service struct {
	q *db.Queries
}

// NewService 构造银行卡域服务。
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: db.New(pool)}
}

// AddParams 添加银行卡参数；可选字段 nil 表示未填。
type AddParams struct {
	CardNo     string
	HolderName string
	BankName   string
	BankCode   *string
	Province   *string
	City       *string
}

// Add 添加一张客户银行卡。
func (s *Service) Add(ctx context.Context, customerID int64, p AddParams) (db.BankCard, error) {
	card, err := s.q.InsertBankCard(ctx, db.InsertBankCardParams{
		CustomerID: customerID,
		CardNo:     p.CardNo,
		HolderName: p.HolderName,
		BankName:   p.BankName,
		BankCode:   p.BankCode,
		Province:   p.Province,
		City:       p.City,
	})
	if err != nil {
		return db.BankCard{}, fmt.Errorf("insert bank card: %w", err)
	}
	return card, nil
}

// List 列出客户全部未删除银行卡，按新到旧排序。
func (s *Service) List(ctx context.Context, customerID int64) ([]db.BankCard, error) {
	cards, err := s.q.ListBankCards(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list bank cards: %w", err)
	}
	return cards, nil
}

// GetOwned 读取客户名下未删除银行卡；不存在、非本人或已删除统一返回 404。
func (s *Service) GetOwned(ctx context.Context, customerID, cardID int64) (db.BankCard, error) {
	card, err := s.q.GetOwnedBankCard(ctx, db.GetOwnedBankCardParams{ID: cardID, CustomerID: customerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.BankCard{}, apierr.New(http.StatusNotFound, apierr.CodeBankCardNotFound, "bank card not found")
	}
	if err != nil {
		return db.BankCard{}, fmt.Errorf("get owned bank card: %w", err)
	}
	return card, nil
}

// SoftDelete 软删客户名下银行卡；SQL 守卫阻止删除在途提现引用的卡。
func (s *Service) SoftDelete(ctx context.Context, customerID, cardID int64) error {
	rows, err := s.q.SoftDeleteBankCard(ctx, db.SoftDeleteBankCardParams{
		ID:         cardID,
		CustomerID: customerID,
	})
	if err != nil {
		return fmt.Errorf("soft delete bank card: %w", err)
	}
	if rows == 0 {
		if _, err := s.GetOwned(ctx, customerID, cardID); err != nil {
			return err
		}
		return apierr.New(http.StatusConflict, apierr.CodeBankCardInUse, "bank card is used by an active withdrawal")
	}
	return nil
}
