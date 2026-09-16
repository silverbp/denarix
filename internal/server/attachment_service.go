// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"errors"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/storage"
)

// attachmentStreamChunkSize caps how much of a file is buffered per
// stream.Send/read at once - well under gRPC's default 4MB max message
// size.
const attachmentStreamChunkSize = 256 * 1024

type attachmentService struct {
	denarixv1.UnimplementedAttachmentServiceServer
	store *db.Store
	blobs *storage.Store
}

func newAttachmentService(store *db.Store, blobs *storage.Store) *attachmentService {
	return &attachmentService{store: store, blobs: blobs}
}

func (s *attachmentService) GetAttachment(ctx context.Context, req *denarixv1.GetAttachmentRequest) (*denarixv1.GetAttachmentResponse, error) {
	a, err := attachmentRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetAttachmentResponse{Attachment: attachmentToProto(a)}, nil
}

func (s *attachmentService) ListAttachments(ctx context.Context, req *denarixv1.ListAttachmentsRequest) (*denarixv1.ListAttachmentsResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListAttachmentsForEntity(ctx, sqlcgen.ListAttachmentsForEntityParams{
		BusinessID: req.GetBusinessId(),
		EntityType: req.GetEntityType(),
		EntityID:   req.GetEntityId(),
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	resp := &denarixv1.ListAttachmentsResponse{}
	for _, a := range rows {
		resp.Attachments = append(resp.Attachments, attachmentToProto(a))
	}
	return resp, nil
}

// UploadAttachment streams a file's bytes in from the client, buffers them
// in memory (fine for the receipt/contract/logo-sized files this is meant
// for; a very large file would need a different approach), and writes them
// to the object-storage backend once the stream ends - so a failed/partial
// upload never creates a half-written object or a dangling attachment row.
// The first message must carry metadata; every message after that must
// carry a chunk.
func (s *attachmentService) UploadAttachment(stream grpc.ClientStreamingServer[denarixv1.UploadAttachmentRequest, denarixv1.UploadAttachmentResponse]) error {
	ctx := stream.Context()
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no authenticated user")
	}

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	meta := first.GetMetadata()
	if meta == nil {
		return status.Error(codes.InvalidArgument, "the first message on an upload stream must carry metadata")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, meta.GetBusinessId(), "MEMBER"); err != nil {
		return err
	}
	if err := validateEntityRef(ctx, s.store.Queries, meta.GetBusinessId(), meta.GetEntityType(), meta.GetEntityId()); err != nil {
		return err
	}

	var buf bytes.Buffer
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if msg.GetMetadata() != nil {
			return status.Error(codes.InvalidArgument, "metadata must only be sent as the first message")
		}
		buf.Write(msg.GetChunk())
	}
	if buf.Len() == 0 {
		return status.Error(codes.InvalidArgument, "no file content received")
	}

	key := storage.NewKey()
	if err := s.blobs.Put(ctx, key, bytes.NewReader(buf.Bytes()), int64(buf.Len()), meta.GetContentType()); err != nil {
		return status.Errorf(codes.Internal, "storing attachment: %v", err)
	}

	size := int64(buf.Len())
	created, err := s.store.Queries.CreateAttachment(ctx, sqlcgen.CreateAttachmentParams{
		BusinessID:       meta.GetBusinessId(),
		EntityType:       meta.GetEntityType(),
		EntityID:         meta.GetEntityId(),
		OriginalFilename: meta.OriginalFilename,
		StorageKey:       key,
		ContentType:      meta.ContentType,
		FileSizeBytes:    &size,
		DisplaySequence:  meta.GetDisplaySequence(),
		CreatedByUserID:  &u.ID,
	})
	if err != nil {
		return translatePgError(err)
	}
	return stream.SendAndClose(&denarixv1.UploadAttachmentResponse{Attachment: attachmentToProto(created)})
}

// DownloadAttachment streams a file's bytes back out. This - not a
// storage_url handed to the client - is the only way to read an
// attachment's content, so every read is gated by denarix's own auth
// (RequireBusinessRole) rather than an unauthenticated storage URL.
func (s *attachmentService) DownloadAttachment(req *denarixv1.DownloadAttachmentRequest, stream grpc.ServerStreamingServer[denarixv1.DownloadAttachmentResponse]) error {
	ctx := stream.Context()
	a, err := attachmentRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return err
	}

	obj, err := s.blobs.Get(ctx, a.StorageKey)
	if err != nil {
		return status.Errorf(codes.Internal, "reading attachment: %v", err)
	}
	defer obj.Close()

	buf := make([]byte, attachmentStreamChunkSize)
	for {
		n, readErr := obj.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&denarixv1.DownloadAttachmentResponse{Chunk: buf[:n]}); sendErr != nil {
				return sendErr
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return status.Errorf(codes.Internal, "reading attachment: %v", readErr)
		}
	}
}

func (s *attachmentService) DeleteAttachment(ctx context.Context, req *denarixv1.DeleteAttachmentRequest) (*denarixv1.DeleteAttachmentResponse, error) {
	if _, err := attachmentRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER"); err != nil {
		return nil, err
	}
	deleted, err := s.store.Queries.DeleteAttachment(ctx, req.GetId())
	if err != nil {
		return nil, translatePgError(err)
	}
	return &denarixv1.DeleteAttachmentResponse{Attachment: attachmentToProto(deleted)}, nil
}

func attachmentToProto(a sqlcgen.Attachment) *denarixv1.Attachment {
	return &denarixv1.Attachment{
		Id:               a.ID,
		BusinessId:       a.BusinessID,
		EntityType:       a.EntityType,
		EntityId:         a.EntityID,
		OriginalFilename: a.OriginalFilename,
		ContentType:      a.ContentType,
		FileSizeBytes:    a.FileSizeBytes,
		DisplaySequence:  a.DisplaySequence,
		CreatedByUserId:  a.CreatedByUserID,
		CreatedAt:        timestampProto(a.CreatedAt),
	}
}
