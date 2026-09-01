package strm

import (
	"context"

	"litepan/internal/domain"
)

func (s *Service) PauseTask(ctx context.Context, id int64, reason domain.PauseReason, message string) error {
	if s == nil || s.repo == nil {
		return nil
	}
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if task.Status == domain.StrmStatusPaused {
		return nil
	}
	_, _ = s.ForceStopTask(ctx, id)
	task.Status = domain.StrmStatusPaused
	task.PausedReason = string(reason)
	if message != "" {
		task.ErrorMessage = message
	}
	return s.repo.Update(ctx, task)
}

func (s *Service) ResumeTask(ctx context.Context, id int64) error {
	if s == nil || s.repo == nil {
		return nil
	}
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if task.Status != domain.StrmStatusPaused {
		return nil
	}
	if !domain.ValidAutoPauseReason(domain.PauseReason(task.PausedReason)) {
		return nil
	}
	task.Status = domain.StrmStatusActive
	task.PausedReason = ""
	task.ErrorMessage = ""
	return s.repo.Update(ctx, task)
}

func (s *Service) PauseByAccount(ctx context.Context, accountID int64, reason domain.PauseReason, message string) (int, error) {
	tasks, err := s.repo.ListByAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, task := range tasks {
		if task == nil || task.Status == domain.StrmStatusPaused {
			continue
		}
		if err := s.PauseTask(ctx, task.ID, reason, message); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Service) ResumeByAccount(ctx context.Context, accountID int64) (int, error) {
	tasks, err := s.repo.ListByAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if task.Status != domain.StrmStatusPaused || !domain.ValidAutoPauseReason(domain.PauseReason(task.PausedReason)) {
			continue
		}
		if err := s.ResumeTask(ctx, task.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
