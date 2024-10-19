package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/mattn/go-sqlite3"
)

const createStmt string = `CREATE TABLE IF NOT EXISTS blocked_domains(
    domain_name TEXT NOT NULL UNIQUE
)`

const existsStmt string = "SELECT EXISTS(SELECT 1 FROM blocked_domains WHERE domain_name = ?)"

const deleteStmt string = "DELETE FROM blocked_domains WHERE domain_name = ?"

const insertStmt string = "INSERT INTO blocked_domains VALUES (?)"

var db *sql.DB

type APIError struct {
	Status     string     `json:"status"`
	Message    string     `json:"message"`
	StatusCode int        `json:"statusCode"`
	Errors     []APIError `json:"additionalErrors,omitempty"`
}

var (
	InvalidJSON         = APIError{StatusCode: http.StatusBadRequest, Message: "Excepted array of strings; got invalid JSON.", Status: "error"}
	InternalServerError = APIError{StatusCode: http.StatusInternalServerError, Message: "Internal server error.", Status: "error"}
)

func ensureJSON(r *http.Request) *APIError {
	if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
		return &APIError{
			StatusCode: http.StatusUnsupportedMediaType,
			Status:     "error",
			Message:    fmt.Sprintf("Excepted content of type \"application/json\", got: \"%s\".", contentType),
		}
	}
	return nil
}

func unexceptedMethod(excepted string, got string) *APIError {
	return &APIError{
		StatusCode: http.StatusMethodNotAllowed,
		Status:     "error",
		Message:    fmt.Sprintf("Excepted method %s, got: %s.", excepted, got),
	}
}

func ensurePOST(r *http.Request) *APIError {
	if r.Method != http.MethodPost {
		return unexceptedMethod(http.MethodPost, r.Method)
	}
	return nil
}

func respondWithError(w http.ResponseWriter, err *APIError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.StatusCode)
	json.NewEncoder(w).Encode(err)
}

func isUniqueConstraintError(err error) bool {
	var sqliteError sqlite3.Error
	if !errors.As(err, &sqliteError) {
		return false
	}
	if !errors.Is(sqliteError.ExtendedCode, sqlite3.ErrConstraintUnique) {
		return false
	}
	return true
}

func ensureValidPOST(r *http.Request) *APIError {
	if err := ensurePOST(r); err != nil {
		return err
	}
	if err := ensureJSON(r); err != nil {
		return err
	}
	return nil
}

func appendHandler(w http.ResponseWriter, r *http.Request) {
	if err := ensureValidPOST(r); err != nil {
		respondWithError(w, err)
		return
	}
	var newDomains []string
	if err := json.NewDecoder(r.Body).Decode(&newDomains); err != nil {
		respondWithError(w, &InvalidJSON)
		return
	}

	if len(newDomains) == 0 {
		respondWithError(w, &APIError{Status: "error", StatusCode: http.StatusBadRequest, Message: "No domains provided."})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		// TODO: Handle error
	}

	stmt, err := tx.Prepare(insertStmt)

	if err != nil {
		// TODO: Handle error
	}

	defer stmt.Close()

	errs := make([]APIError, 0, len(newDomains))

	for index, name := range newDomains {
		_, err := stmt.Exec(name)
		if err != nil {
			if isUniqueConstraintError(err) {
				errs = append(errs, APIError{
					StatusCode: http.StatusConflict,
					Message:    fmt.Sprintf("Domain \"%s\" (%d in the array) is already in the database.", name, index),
					Status:     "error",
				})
				continue
			}
			tx.Rollback()
			respondWithError(w, &InternalServerError)
			return
		}
	}
	tx.Commit()
	if len(errs) == len(newDomains) {
		respondWithError(w, &APIError{Status: "error", StatusCode: http.StatusConflict, Message: "All of the domains are already in the database."})
	} else if len(errs) == 0 {
		respondWithError(w, &APIError{StatusCode: http.StatusCreated, Message: "Succesfully created all of the domains.", Status: "success"})
	} else {
		respondWithError(w, &APIError{Status: "partial", StatusCode: http.StatusCreated, Message: "Some of the domains are already in the database.", Errors: errs})
	}
}

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	if err := ensureValidPOST(r); err != nil {
		respondWithError(w, err)
		return
	}
	var removedDomains []string
	if err := json.NewDecoder(r.Body).Decode(&removedDomains); err != nil {
		respondWithError(w, &InvalidJSON)
		return
	}

	if len(removedDomains) == 0 {
		respondWithError(w, &APIError{Status: "error", StatusCode: http.StatusBadRequest, Message: "No domains provided."})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		// TODO: Handle error
	}

	stmt, err := tx.Prepare(deleteStmt)

	if err != nil {
		// TODO: Handle error
	}

	defer stmt.Close()

	errs := make([]APIError, 0, len(removedDomains))

	for index, name := range removedDomains {
		result, err := stmt.Exec(name)
		if err != nil {
			tx.Rollback()
			respondWithError(w, &InternalServerError)
			return
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			errs = append(errs, APIError{
				Status:     "error",
				StatusCode: http.StatusNotFound,
				Message:    fmt.Sprintf("Domain \"%s\" (%d in the array) isn't in the database.", name, index),
			})
		}
	}
	tx.Commit()
	if len(errs) == len(removedDomains) {
		respondWithError(w, &APIError{Status: "error", StatusCode: http.StatusNotFound, Message: "All of the domains aren't in the database."})
	} else if len(errs) == 0 {
		respondWithError(w, &APIError{StatusCode: http.StatusOK, Message: "Succesfully removed all of the specified domains.", Status: "success"})
	} else {
		respondWithError(w, &APIError{Status: "partial", StatusCode: http.StatusOK, Message: "Some of the domains aren't in the database.", Errors: errs})
	}
}

type CheckSchema struct {
	Included bool `json:"isIncluded"`
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondWithError(w, unexceptedMethod(http.MethodGet, r.Method))
		return
	}

	domain := r.URL.Query().Get("domain")
	if domain == "" {
		respondWithError(w, &APIError{
			Status:     "error",
			StatusCode: http.StatusBadRequest,
			Message:    "Parameter \"domain\" wasn't provided in the query!",
		})
		return
	}

	var successCode int

	db.QueryRowContext(r.Context(), existsStmt, domain).Scan(&successCode)

	var schema CheckSchema

	if successCode == 0 {
		schema.Included = false
	} else {
		schema.Included = true
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(schema)
}

func main() {
	var err error
	db, err = sql.Open("sqlite3", "database/db.db")

	if err != nil {
		log.Fatalf("Database name is invalid: %v\n", err)
	}

	defer db.Close()

	_, err = db.Exec(createStmt)
	if err != nil {
		log.Fatalf("Execution of {createStmt} failed: %v\n", err)
	}

	http.HandleFunc("/domains/append", appendHandler)
	http.HandleFunc("/domains/check", checkHandler)
	http.HandleFunc("/domains/delete", deleteHandler)

	log.Fatal(http.ListenAndServe(":8000", nil))
}
