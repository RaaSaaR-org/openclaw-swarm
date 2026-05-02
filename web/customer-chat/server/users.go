package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// userRecord mirrors the shape written by customer-center.
// Only the fields needed for auth are kept.
type userRecord struct {
	Email             string `json:"email"`
	PasswordHash      string `json:"passwordHash"`
	CreatedAt         string `json:"createdAt"`
	PasswordUpdatedAt string `json:"passwordUpdatedAt"`
}

func (s *server) readUsers(ctx context.Context, slug string) ([]userRecord, error) {
	if s.core == nil {
		return nil, errors.New("no kube client")
	}
	sec, err := s.core.CoreV1().Secrets(s.namespace).Get(ctx, "kai-"+slug+"-users", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("users secret missing for %s", slug)
		}
		return nil, err
	}
	raw := sec.Data["users.json"]
	if len(raw) == 0 {
		return nil, nil
	}
	var users []userRecord
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, fmt.Errorf("parse users.json: %w", err)
	}
	return users, nil
}
