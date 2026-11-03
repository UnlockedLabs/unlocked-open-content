package database

import (
	"errors"
	"fmt"
	"net/http"

	"gorm.io/gorm"
)

// DBError is a failed query, with the status and message to answer with.
type DBError struct {
	Status      int
	Message     string
	InternalErr error
}

func (e DBError) Error() string {
	return fmt.Sprintf("db error: %s", e.InternalErr.Error())
}

func NewDBError(err error, msg string) DBError {
	dbErr := DBError{Message: msg, InternalErr: err}
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound),
		errors.Is(err, gorm.ErrInvalidData),
		errors.Is(err, gorm.ErrEmptySlice),
		errors.Is(err, gorm.ErrInvalidField),
		errors.Is(err, gorm.ErrInvalidValueOfLength):
		dbErr.Status = http.StatusBadRequest
	case errors.Is(err, gorm.ErrPrimaryKeyRequired),
		errors.Is(err, gorm.ErrForeignKeyViolated),
		errors.Is(err, gorm.ErrDuplicatedKey),
		errors.Is(err, gorm.ErrCheckConstraintViolated):
		dbErr.Status = http.StatusUnprocessableEntity
	default:
		dbErr.Status = http.StatusInternalServerError
	}
	return dbErr
}

func newGetRecordsDBError(err error, table string) DBError {
	return NewDBError(err, fmt.Sprintf("error getting %s", table))
}

func newDeleteDBError(err error, table string) DBError {
	return NewDBError(err, fmt.Sprintf("error deleting %s", table))
}

func newCreateDBError(err error, table string) DBError {
	return NewDBError(err, fmt.Sprintf("error creating %s", table))
}

func newUpdateDBError(err error, table string) DBError {
	return NewDBError(err, fmt.Sprintf("error updating %s", table))
}

func newNotFoundDBError(err error, table string) DBError {
	return NewDBError(err, fmt.Sprintf("%s not found", table))
}
