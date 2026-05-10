/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	swarmv1alpha2 "github.com/emai-ai/swarm-operator/api/v1alpha2"
)

// usersSecretName returns the per-customer Secret holding the email + password-hash list.
func usersSecretName(slug string) string {
	return childName(slug) + "-users"
}

// chatBridgeSecretName returns the per-customer Secret holding the chat-bridge device keypair + JWT secret.
func chatBridgeSecretName(slug string) string {
	return childName(slug) + "-chat-bridge"
}

// buildUsersSecret creates the empty users-list Secret for a customer.
// Shape: { "users.json": "[]" } — populated by workspace's admin UI.
func buildUsersSecret(kai *swarmv1alpha2.KaiInstance, slug string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      usersSecretName(slug),
			Namespace: kai.Namespace,
			Labels:    commonLabels(slug),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"users.json": []byte("[]"),
		},
	}
}

// buildChatBridgeSecret generates the chat-bridge device keypair and JWT signing secret.
// Keys:
//   - device-id          hex SHA-256 of the raw 32-byte ed25519 public key (matches existing webchat fingerprint format)
//   - device-public      base64url-encoded ed25519 public key
//   - device-private     base64url-encoded ed25519 private key (64 bytes seed||pub)
//   - jwt-secret         32 random bytes, hex-encoded
func buildChatBridgeSecret(kai *swarmv1alpha2.KaiInstance, slug string) (*corev1.Secret, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 keypair: %w", err)
	}

	idHash := sha256.Sum256(pub)
	deviceID := hex.EncodeToString(idHash[:])

	jwt := make([]byte, 32)
	if _, err := rand.Read(jwt); err != nil {
		return nil, fmt.Errorf("generate jwt secret: %w", err)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      chatBridgeSecretName(slug),
			Namespace: kai.Namespace,
			Labels:    commonLabels(slug),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"device-id":      []byte(deviceID),
			"device-public":  []byte(base64.RawURLEncoding.EncodeToString(pub)),
			"device-private": []byte(base64.RawURLEncoding.EncodeToString(priv)),
			"jwt-secret":     []byte(hex.EncodeToString(jwt)),
		},
	}, nil
}

// reconcileUsersSecret creates the users Secret if missing. Never overwrites — admin writes are
// authoritative once the Secret exists.
func (r *KaiInstanceReconciler) reconcileUsersSecret(ctx context.Context, kai *swarmv1alpha2.KaiInstance, slug string) error {
	desired := buildUsersSecret(kai, slug)
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	return err
}

// reconcileChatBridgeSecret creates the chat-bridge Secret if missing. Never overwrites — the
// device keypair is sticky for the lifetime of the KaiInstance (rotation is a separate flow).
func (r *KaiInstanceReconciler) reconcileChatBridgeSecret(ctx context.Context, kai *swarmv1alpha2.KaiInstance, slug string) error {
	var existing corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Name: chatBridgeSecretName(slug), Namespace: kai.Namespace}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	desired, err := buildChatBridgeSecret(kai, slug)
	if err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, desired)
}
