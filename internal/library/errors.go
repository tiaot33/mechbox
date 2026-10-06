package library

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"

	"github.com/mattn/go-sqlite3"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/discovery"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/download"
	"github.com/tiaot33/skills-tui/internal/library/internal/source/local"
)

func typedError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	if missing, ok := errors.AsType[*source.SkillNotFoundError](err); ok {
		return &Error{Code: NotFound, Message: err.Error(), Details: ErrorDetails{Name: missing.Name}, Err: err}
	}
	if errors.Is(err, local.ErrNotDirectory) {
		return argumentError("read local source", "path", "path is not a directory")
	}
	if errors.Is(err, download.ErrInvalidURL) {
		return argumentError("fetch source", "url", "invalid source URL")
	}
	code := Internal
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = Canceled
	case errors.Is(err, discovery.ErrTooLarge):
		return fetchError(SourceInvalid, "read source", err)
	case errors.Is(err, discovery.ErrNoCandidates), errors.Is(err, source.ErrInvalid):
		code = SourceInvalid
	case errors.Is(err, source.ErrUnreachable):
		code = SourceUnreachable
	case errors.Is(err, source.ErrPermission):
		code = Permission
	case errors.Is(err, sql.ErrNoRows):
		code = NotFound
	case errors.Is(err, fs.ErrPermission):
		code = Permission
	case errors.Is(err, fs.ErrNotExist):
		code = SourceUnreachable
	default:
		code = sqliteErrorCode(err)
	}
	return &Error{Code: code, Message: err.Error(), Err: err}
}

func sqliteErrorCode(err error) ErrorCode {
	sqliteErr, ok := errors.AsType[sqlite3.Error](err)
	if !ok {
		return Internal
	}
	if sqliteErr.Code == sqlite3.ErrBusy || sqliteErr.Code == sqlite3.ErrLocked {
		return Busy
	}
	switch sqliteErr.ExtendedCode {
	case sqlite3.ErrConstraintUnique, sqlite3.ErrConstraintPrimaryKey:
		return AlreadyExists
	case sqlite3.ErrConstraintForeignKey:
		return NotFound
	default:
		return Internal
	}
}

func fetchError(code ErrorCode, operation string, err error) error {
	return &Error{Code: code, Message: operation + ": " + err.Error(), Err: err}
}

func fetchErrorCode(err error) ErrorCode {
	if errors.Is(err, fs.ErrPermission) {
		return Permission
	}
	return SourceUnreachable
}

func argumentError(operation, field, reason string) *Error {
	return &Error{Code: InvalidArgument, Message: operation + ": " + reason, Details: ErrorDetails{Field: field}}
}

func existsError(operation, reason string, details ErrorDetails) *Error {
	return &Error{Code: AlreadyExists, Message: operation + ": " + reason, Details: details}
}

func preconditionError(operation, reason string, details ErrorDetails) *Error {
	return &Error{Code: FailedPrecondition, Message: operation + ": " + reason, Details: details}
}

func validateFolderPolicy(operation string, policy FolderPolicy) error {
	switch policy {
	case "", Keep, Remove:
		return nil
	default:
		return argumentError(operation, "folders", "invalid folders option")
	}
}

func validateMemberRemoval(operation string, policy MemberRemovalPolicy) error {
	switch policy {
	case "", KeepAsDirectInstall, UninstallMember:
		return nil
	default:
		return argumentError(operation, "if_no_other_install", "invalid if_no_other_install option")
	}
}

func validateConflictPolicy(operation string, policy ConflictPolicy) error {
	switch policy {
	case "", Fail, Overwrite:
		return nil
	default:
		return argumentError(operation, "on_conflict", "invalid on_conflict option")
	}
}
