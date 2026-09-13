package library

import (
	"database/sql"
	"errors"
	"io/fs"
)

func typedError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*Error](err); ok {
		return err
	}
	code := Internal
	if errors.Is(err, sql.ErrNoRows) {
		code = NotFound
	} else if errors.Is(err, fs.ErrPermission) {
		code = Permission
	} else if errors.Is(err, fs.ErrNotExist) {
		code = SourceUnreachable
	}
	return &Error{Code: code, Message: err.Error(), Err: err}
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
