package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// 坐标按视口内可拖动范围归一化，换设备后仍能显示在视口内。
type RechargeReviewPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func (p RechargeReviewPosition) Valid() bool {
	return p.X >= 0 && p.X <= 1 && p.Y >= 0 && p.Y <= 1
}

func rechargeReviewPositionKey(userID int64) string {
	return fmt.Sprintf("balance_center_recharge_review_position:%d", userID)
}

func (s *BalanceCenterService) GetRechargeReviewPosition(ctx context.Context, userID int64) (*RechargeReviewPosition, error) {
	raw, err := s.settingRepo.GetValue(ctx, rechargeReviewPositionKey(userID))
	if errors.Is(err, ErrSettingNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var position RechargeReviewPosition
	if err := json.Unmarshal([]byte(raw), &position); err != nil || !position.Valid() {
		return nil, nil // 无效的旧偏好不阻止展示默认位置。
	}
	return &position, nil
}

func (s *BalanceCenterService) SaveRechargeReviewPosition(ctx context.Context, userID int64, position RechargeReviewPosition) error {
	if userID <= 0 || !position.Valid() {
		return errors.New("审核提醒位置无效")
	}
	raw, err := json.Marshal(position)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, rechargeReviewPositionKey(userID), string(raw))
}
