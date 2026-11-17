package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"devdock/internal/runner"
	"devdock/internal/store"
)

// apiError 是统一错误信封。
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

// 稳定的错误码词表，HTTP 与 CLI 共用。
const (
	CodeNotFound       = "not_found"
	CodeConflict       = "conflict"
	CodePortInUse      = "port_in_use"
	CodeSpawnFailed    = "spawn_failed"
	CodeAlreadyRunning = "already_running"
	CodeNotRunning     = "not_running"
	CodeConfigInvalid  = "config_invalid"
	CodeUnauthorized   = "unauthorized"
	CodeInternal       = "internal"
)

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if v != nil {
		_ = enc.Encode(v)
	}
}

// writeError 输出统一错误信封。
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

// mapError 把内部错误映射为 HTTP 状态码与稳定错误码。
// 用户可见文案保持中文、说人话，不带 HTTP 术语和 emoji。
func mapError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusBadRequest, CodeConfigInvalid, err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeConfigInvalid, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, runner.ErrAlreadyRunning):
		writeError(w, http.StatusConflict, CodeAlreadyRunning, "该项目已在运行，请先停止再启动")
	case errors.Is(err, runner.ErrNotRunning):
		writeError(w, http.StatusConflict, CodeNotRunning, "该项目当前未在运行")
	case errors.Is(err, runner.ErrPortInUse):
		writeError(w, http.StatusConflict, CodePortInUse, err.Error()+"，请换一个端口或先停掉占用者")
	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, err.Error())
	}
}
