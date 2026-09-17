// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package server wires the gRPC server: the Postgres-backed Store, the auth
// interceptor, and one implementation per proto item.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/config"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/storage"
)

type Server struct {
	cfg        config.Config
	store      *db.Store
	grpc       *grpc.Server
	httpServer *http.Server
}

func New(ctx context.Context, cfg config.Config) (*Server, error) {
	store, err := db.NewStore(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, err
	}

	blobs, err := storage.New(cfg.StorageEndpoint, cfg.StorageAccessKey, cfg.StorageSecretKey, cfg.StorageBucket, cfg.StorageUseSSL)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("configuring object storage: %w", err)
	}
	if err := blobs.EnsureBucket(ctx); err != nil {
		store.Close()
		return nil, fmt.Errorf("configuring object storage: %w", err)
	}

	if cfg.BootstrapAdminEmail != "" {
		res, err := auth.EnsureBootstrapAdmin(ctx, store, cfg.BootstrapAdminEmail, cfg.BootstrapAdminReset)
		if err != nil {
			store.Close()
			return nil, fmt.Errorf("bootstrapping global admin: %w", err)
		}
		if res != nil && res.User != nil {
			slog.Info("bootstrapped global admin", "user_id", res.User.ID, "email", res.User.Email, "reset", res.Reset)
		}
		if res != nil && res.EnrollmentToken != "" {
			slog.Warn("passkey enrollment token minted for the global admin - shown once, redeem it now",
				"email", cfg.BootstrapAdminEmail,
				"revokes_existing_passkeys", res.Reset,
				"redeem", fmt.Sprintf("dxctl login --token %s", res.EnrollmentToken),
				"or_open", fmt.Sprintf("%s/auth/start?token=%s", cfg.PublicBaseURL, res.EnrollmentToken))
			if cfg.BootstrapAdminReset {
				slog.Warn("DENARIX_BOOTSTRAP_ADMIN_RESET is set - unset it and restart once you have re-enrolled, or the next start will mint another reset token")
			}
		}
	}

	verifyAccessToken := func(token string) (*auth.User, error) {
		return auth.VerifyAccessToken(cfg.JWTSecret, token)
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(auth.UnaryInterceptor(verifyAccessToken)),
		grpc.StreamInterceptor(auth.StreamInterceptor(verifyAccessToken)),
	)

	denarixv1.RegisterBusinessServiceServer(grpcServer, newBusinessService(store))
	denarixv1.RegisterLedgerAccountServiceServer(grpcServer, newLedgerAccountService(store))
	denarixv1.RegisterLedgerTransactionServiceServer(grpcServer, newLedgerTransactionService(store))
	denarixv1.RegisterReportingServiceServer(grpcServer, newReportingService(store))
	denarixv1.RegisterPeriodCloseServiceServer(grpcServer, newPeriodCloseService(store))
	denarixv1.RegisterContactServiceServer(grpcServer, newContactService(store))
	denarixv1.RegisterItemServiceServer(grpcServer, newItemService(store))
	denarixv1.RegisterTaxRateServiceServer(grpcServer, newTaxRateService(store))
	denarixv1.RegisterEstimateServiceServer(grpcServer, newEstimateService(store))
	denarixv1.RegisterInvoiceServiceServer(grpcServer, newInvoiceService(store))
	denarixv1.RegisterPaymentServiceServer(grpcServer, newPaymentService(store))
	denarixv1.RegisterBankStatementServiceServer(grpcServer, newBankStatementService(store))
	denarixv1.RegisterEntityContextServiceServer(grpcServer, newEntityContextService(store))
	denarixv1.RegisterAttachmentServiceServer(grpcServer, newAttachmentService(store, blobs))
	denarixv1.RegisterAuthServiceServer(grpcServer, newAuthService(store, cfg))
	denarixv1.RegisterUserServiceServer(grpcServer, newUserService(store))
	denarixv1.RegisterReferenceDataServiceServer(grpcServer, newReferenceDataService(store))

	// Reflection lets grpcurl/grpcui introspect the API without shipping
	// .proto files alongside every deploy. Revisit gating this before a
	// real production launch.
	reflection.Register(grpcServer)

	authMux, err := newAuthMux(store, cfg)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("configuring auth HTTP server: %w", err)
	}
	httpServer := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: authMux,
	}

	return &Server{cfg: cfg, store: store, grpc: grpcServer, httpServer: httpServer}, nil
}

// Run serves both listeners until either exits; an error from one stops
// the other.
func (s *Server) Run() error {
	lis, err := net.Listen("tcp", s.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.cfg.GRPCAddr, err)
	}

	errCh := make(chan error, 2)
	go func() {
		slog.Info("denarix gRPC server listening", "addr", s.cfg.GRPCAddr)
		errCh <- s.grpc.Serve(lis)
	}()
	go func() {
		slog.Info("denarix auth HTTP server listening", "addr", s.cfg.HTTPAddr)
		err := s.httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	return <-errCh
}

func (s *Server) Close() {
	s.grpc.GracefulStop()
	_ = s.httpServer.Close()
	s.store.Close()
}
