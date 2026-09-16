// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/output"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

// entity_context and attachment are two resources that share one command
// group (`context`): both are always scoped to one entity_type + entity_id
// rather than listed business-wide the way every other noun is. The group
// noun carries the name; the two nouns below carry the table columns.
var contextGroupNoun = resource.Noun{Singular: "context"}

var entityContextNoun = resource.Noun{
	Singular: "entity-context",
	Plural:   "entity-context rows",
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.EntityContext).GetId),
		resource.Str("TYPE", (*denarixv1.EntityContext).GetContextType),
		resource.Str("CONTENT", (*denarixv1.EntityContext).GetContent),
		resource.Bool("SUPERSEDED", func(ec *denarixv1.EntityContext) bool { return ec.SupersededById != nil }),
	},
}

var attachmentNoun = resource.Noun{
	Singular: "attachment",
	Plural:   "attachments",
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.Attachment).GetId),
		resource.Str("FILENAME", (*denarixv1.Attachment).GetOriginalFilename),
		resource.Int("SIZE", (*denarixv1.Attachment).GetFileSizeBytes),
		resource.Str("CONTENT-TYPE", (*denarixv1.Attachment).GetContentType),
	},
}

func newContextCmd() *cobra.Command {
	root := newGroupCmd(contextGroupNoun, "Manage AI/user context and attachments for an entity")
	root.AddCommand(
		newContextListCmd(),
		newMutateCmd(entityContextNoun, "get", resource.Doc{
			Summary:  "Get one entity-context row by id",
			Examples: []resource.Example{{Cmd: "dxctl context get 7"}},
		}, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewEntityContextServiceClient(r.conn).GetEntityContext(r.ctx, &denarixv1.GetEntityContextRequest{Id: id})
			return resp.GetEntityContext(), err
		}),
		newMutateCmd(attachmentNoun, "get-attachment", resource.Doc{
			Summary:  "Get one attachment's metadata by id",
			Detail:   "Metadata only - use `context download` to read its file content.",
			Examples: []resource.Example{{Cmd: "dxctl context get-attachment 9"}},
		}, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewAttachmentServiceClient(r.conn).GetAttachment(r.ctx, &denarixv1.GetAttachmentRequest{Id: id})
			return resp.GetAttachment(), err
		}),
		newContextNoteCmd(),
		newMutateCmd(entityContextNoun, "remove-note", resource.Doc{
			Summary: "Delete a context/note row outright",
			Detail: "For a note that shouldn't have been created at all. To correct a stale note " +
				"without losing the trail, use `context note --supersedes <id>` instead.",
			Examples: []resource.Example{{Cmd: "dxctl context remove-note 7"}},
		}, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewEntityContextServiceClient(r.conn).DeleteEntityContext(r.ctx, &denarixv1.DeleteEntityContextRequest{Id: id})
			return resp.GetEntityContext(), err
		}),
		newContextAttachCmd(),
		newContextDownloadCmd(),
		newMutateCmd(attachmentNoun, "remove-attachment", resource.Doc{
			Summary:  "Delete an attachment",
			Examples: []resource.Example{{Cmd: "dxctl context remove-attachment 9"}},
		}, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewAttachmentServiceClient(r.conn).DeleteAttachment(r.ctx, &denarixv1.DeleteAttachmentRequest{Id: id})
			return resp.GetAttachment(), err
		}),
	)
	return root
}

// newContextDownloadCmd is hand-written: it writes a file rather than
// printing a resource.
func newContextDownloadCmd() *cobra.Command {
	var id int64
	var out string

	cmd := &cobra.Command{
		Use:  "download",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := dialRun(cmd)
			if err != nil {
				return err
			}
			defer r.conn.Close()

			stream, err := denarixv1.NewAttachmentServiceClient(r.conn).DownloadAttachment(r.ctx, &denarixv1.DownloadAttachmentRequest{Id: id})
			if err != nil {
				return err
			}

			f, err := os.Create(out)
			if err != nil {
				return fmt.Errorf("creating %s: %w", out, err)
			}
			defer f.Close()

			for {
				msg, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return err
				}
				if _, err := f.Write(msg.GetChunk()); err != nil {
					return fmt.Errorf("writing %s: %w", out, err)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "downloaded attachment %d to %s\n", id, out)
			return nil
		},
	}
	cmd.Flags().Int64Var(&id, "id", 0, "attachment id (required)")
	cmd.Flags().StringVar(&out, "out", "", "local path to write the file to (required)")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("out")
	resource.Doc{
		Summary: "Download an attachment's file content",
		Detail: "Streams the attachment's bytes through AttachmentService.DownloadAttachment - " +
			"the only way to read a file's content; denarix never hands out a direct storage URL.",
		Examples: []resource.Example{{Cmd: "dxctl context download --id 9 --out invoice.pdf"}},
	}.Apply(cmd)
	return cmd
}

func newContextNoteCmd() *cobra.Command {
	var entityType, contextType, content, metadataJSON, source, confidence string
	var entityID int64
	var supersedesRaw []string

	cmd := newNoArgCmd(entityContextNoun, "note", resource.Doc{
		Summary:  "Attach AI-generated or user context to any entity",
		Examples: []resource.Example{{Cmd: `dxctl context note --entity-type invoice --entity-id 42 --content "customer requested a discount"`}},
	}, func(r run) (proto.Message, error) {
		var supersedes []int64
		for _, s := range supersedesRaw {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid --supersedes %q: %w", s, err)
			}
			supersedes = append(supersedes, n)
		}
		resp, err := denarixv1.NewEntityContextServiceClient(r.conn).CreateEntityContext(r.ctx, &denarixv1.CreateEntityContextRequest{
			BusinessId:    r.businessID,
			EntityType:    entityType,
			EntityId:      entityID,
			ContextType:   contextType,
			Content:       content,
			SupersedesIds: supersedes,
			MetadataJson:  r.optString("metadata", &metadataJSON),
			Source:        r.optString("source", &source),
			Confidence:    r.optDecimal("confidence", &confidence),
		})
		return resp.GetEntityContext(), err
	})
	cmd.Flags().StringVar(&entityType, "entity-type", "", "target entity type, e.g. invoice, contact, ledger_transaction (required)")
	cmd.Flags().Int64Var(&entityID, "entity-id", 0, "target entity id (required)")
	cmd.Flags().StringVar(&contextType, "context-type", "user_note", "summary, categorization_hint, anomaly, or user_note")
	cmd.Flags().StringVar(&content, "content", "", "context content (required)")
	cmd.Flags().StringVar(&metadataJSON, "metadata", "", "arbitrary metadata, as a JSON object string")
	cmd.Flags().StringVar(&source, "source", "", "source label, e.g. an MCP session id")
	cmd.Flags().StringVar(&confidence, "confidence", "", "confidence, e.g. 0.9")
	cmd.Flags().StringArrayVar(&supersedesRaw, "supersedes", nil, "id of an older entity-context row this one rolls up (repeatable)")
	_ = cmd.MarkFlagRequired("entity-type")
	_ = cmd.MarkFlagRequired("entity-id")
	_ = cmd.MarkFlagRequired("content")
	return cmd
}

func newContextAttachCmd() *cobra.Command {
	var entityType, path, filename, contentType string
	var entityID int64

	cmd := newNoArgCmd(attachmentNoun, "attach", resource.Doc{
		Summary: "Upload a file and attach it to any entity",
		Detail: "Streams a local file's bytes to denarix through AttachmentService.UploadAttachment, " +
			"which stores it in denarix's own object-storage backend - not a caller-supplied URL.",
		Examples: []resource.Example{{Cmd: "dxctl context attach --entity-type invoice --entity-id 42 --file receipt.pdf"}},
	}, func(r run) (proto.Message, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", path, err)
		}
		defer f.Close()

		if filename == "" {
			filename = filepath.Base(path)
		}

		stream, err := denarixv1.NewAttachmentServiceClient(r.conn).UploadAttachment(r.ctx)
		if err != nil {
			return nil, err
		}
		meta := &denarixv1.UploadAttachmentMetadata{
			BusinessId:       r.businessID,
			EntityType:       entityType,
			EntityId:         entityID,
			OriginalFilename: &filename,
			ContentType:      r.optString("content-type", &contentType),
		}
		if err := stream.Send(&denarixv1.UploadAttachmentRequest{Data: &denarixv1.UploadAttachmentRequest_Metadata{Metadata: meta}}); err != nil {
			return nil, err
		}

		buf := make([]byte, 256*1024)
		for {
			n, readErr := f.Read(buf)
			if n > 0 {
				if sendErr := stream.Send(&denarixv1.UploadAttachmentRequest{Data: &denarixv1.UploadAttachmentRequest_Chunk{Chunk: buf[:n]}}); sendErr != nil {
					return nil, sendErr
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return nil, fmt.Errorf("reading %s: %w", path, readErr)
			}
		}

		resp, err := stream.CloseAndRecv()
		return resp.GetAttachment(), err
	})
	cmd.Flags().StringVar(&entityType, "entity-type", "", "target entity type, e.g. invoice, contact (required)")
	cmd.Flags().Int64Var(&entityID, "entity-id", 0, "target entity id (required)")
	cmd.Flags().StringVar(&path, "file", "", "local path of the file to upload (required)")
	cmd.Flags().StringVar(&filename, "filename", "", "original filename (default: the uploaded file's base name)")
	cmd.Flags().StringVar(&contentType, "content-type", "", "MIME content type")
	_ = cmd.MarkFlagRequired("entity-type")
	_ = cmd.MarkFlagRequired("entity-id")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// newContextListCmd is hand-written: it prints two lists (context rows and
// attachments) for one entity.
func newContextListCmd() *cobra.Command {
	var entityType string
	var entityID int64
	var includeSuperseded bool

	cmd := &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := dialRun(cmd)
			if err != nil {
				return err
			}
			defer r.conn.Close()

			ctxResp, err := denarixv1.NewEntityContextServiceClient(r.conn).ListEntityContext(r.ctx, &denarixv1.ListEntityContextRequest{
				BusinessId:        r.businessID,
				EntityType:        entityType,
				EntityId:          entityID,
				IncludeSuperseded: includeSuperseded,
			})
			if err != nil {
				return err
			}
			attResp, err := denarixv1.NewAttachmentServiceClient(r.conn).ListAttachments(r.ctx, &denarixv1.ListAttachmentsRequest{
				BusinessId: r.businessID,
				EntityType: entityType,
				EntityId:   entityID,
			})
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			if flagOutput == output.FormatTable {
				fmt.Fprintln(w, "CONTEXT")
			} else {
				fmt.Fprintln(w, "--- entity_context ---")
			}
			if err := output.PrintList(w, flagOutput, toMessages(ctxResp.GetEntityContexts()), entityContextNoun.Columns); err != nil {
				return err
			}
			if flagOutput == output.FormatTable {
				fmt.Fprintln(w)
				fmt.Fprintln(w, "ATTACHMENTS")
			} else {
				fmt.Fprintln(w, "--- attachments ---")
			}
			return output.PrintList(w, flagOutput, toMessages(attResp.GetAttachments()), attachmentNoun.Columns)
		},
	}
	cmd.Flags().StringVar(&entityType, "entity-type", "", "target entity type, e.g. invoice, contact (required)")
	cmd.Flags().Int64Var(&entityID, "entity-id", 0, "target entity id (required)")
	cmd.Flags().BoolVar(&includeSuperseded, "include-superseded", false, "include entity-context rows that have been superseded")
	_ = cmd.MarkFlagRequired("entity-type")
	_ = cmd.MarkFlagRequired("entity-id")
	resource.Doc{
		Summary:  "List entity-context and attachments for one entity",
		Examples: []resource.Example{{Cmd: "dxctl context list --entity-type invoice --entity-id 42"}},
	}.Apply(cmd)
	return cmd
}
