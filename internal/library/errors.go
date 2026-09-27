package library

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"

	"github.com/mattn/go-sqlite3"
)

func typedError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	code := Internal
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = Canceled
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

// sqliteErrorCode 是兜底映射:另一进程占着写锁(主码 SQLITE_BUSY / SQLITE_LOCKED,按主码匹配以覆盖
// BUSY_SNAPSHOT、BUSY_RECOVERY 等扩展码)→ Busy;漏掉显式检查的 UNIQUE / PRIMARY KEY → AlreadyExists,
// FOREIGN KEY → NotFound;其余 Internal。
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

func validateInstallationsPolicy(operation string, policy InstallationsPolicy) error {
	switch policy {
	case "", Keep, Remove:
		return nil
	default:
		return argumentError(operation, "installations", "invalid installations option")
	}
}

func validateMemberRemoval(operation string, policy MemberRemovalPolicy) error {
	switch policy {
	case "", KeepInstallation, WithdrawInstallation:
		return nil
	default:
		return argumentError(operation, "installations", "invalid installations option")
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
