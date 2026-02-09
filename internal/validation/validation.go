// Package validation provides input validation middleware for the Alancoin API.
package validation

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// MaxRequestSize is the maximum request body size (1MB)
const MaxRequestSize = 1 << 20 // 1MB

// MaxStringLength is the maximum length for string fields
const MaxStringLength = 10000

var (
	// ethAddressRegex validates Ethereum addresses
	ethAddressRegex = regexp.MustCompile(`^0x[a-fA-F0-9]{40}$`)
	// hexRegex validates hex strings (for signatures, etc)
	hexRegex = regexp.MustCompile(`^(0x)?[a-fA-F0-9]+$`)
)

// RequestSizeMiddleware limits request body size
func RequestSizeMiddleware(maxSize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSize)
		c.Next()
	}
}

// IsValidEthAddress checks if a string is a valid Ethereum address
func IsValidEthAddress(addr string) bool {
	return ethAddressRegex.MatchString(addr)
}

// IsValidHex checks if a string is valid hex
func IsValidHex(s string) bool {
	return hexRegex.MatchString(s)
}

// SanitizeString removes dangerous characters and limits length
func SanitizeString(s string, maxLen int) string {
	// Trim whitespace
	s = strings.TrimSpace(s)

	// Limit length
	if len(s) > maxLen {
		s = s[:maxLen]
	}

	// Remove null bytes
	s = strings.ReplaceAll(s, "\x00", "")

	return s
}

// SanitizeAddress normalizes an Ethereum address
func SanitizeAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	addr = strings.ToLower(addr)

	// Ensure 0x prefix
	if !strings.HasPrefix(addr, "0x") && len(addr) == 40 {
		addr = "0x" + addr
	}

	return addr
}

// ValidationError represents a validation error
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationErrors is a collection of validation errors
type ValidationErrors []ValidationError

// Error implements the error interface
func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "validation failed"
	}
	return e[0].Field + ": " + e[0].Message
}

// Validate validates a request and returns errors
func Validate(validators ...func() *ValidationError) ValidationErrors {
	var errors ValidationErrors
	for _, v := range validators {
		if err := v(); err != nil {
			errors = append(errors, *err)
		}
	}
	return errors
}

// Required checks if a field is non-empty
func Required(field, value string) func() *ValidationError {
	return func() *ValidationError {
		if strings.TrimSpace(value) == "" {
			return &ValidationError{Field: field, Message: "is required"}
		}
		return nil
	}
}

// ValidAddress checks if a field is a valid Ethereum address
func ValidAddress(field, value string) func() *ValidationError {
	return func() *ValidationError {
		if value == "" {
			return nil // Use Required for required fields
		}
		if !IsValidEthAddress(value) {
			return &ValidationError{Field: field, Message: "must be a valid Ethereum address (0x...)"}
		}
		return nil
	}
}

// MaxLength checks if a field exceeds max length
func MaxLength(field, value string, max int) func() *ValidationError {
	return func() *ValidationError {
		if len(value) > max {
			return &ValidationError{Field: field, Message: "exceeds maximum length"}
		}
		return nil
	}
}

// AddressParamMiddleware validates the :address URL parameter on routes that use it.
// Apply to route groups that include :address params to reject malformed addresses early.
func AddressParamMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		addr := c.Param("address")
		if addr != "" && !IsValidEthAddress(addr) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_address",
				"message": "address must be a valid Ethereum address (0x + 40 hex chars)",
			})
			return
		}
		c.Next()
	}
}

// ValidAmount checks if a value is a valid USDC amount (must be positive)
func ValidAmount(field, value string) func() *ValidationError {
	return func() *ValidationError {
		if value == "" {
			return nil
		}
		// Should be a positive decimal number with at most one decimal point
		decimalCount := 0
		hasNonZero := false
		for i, c := range value {
			if c == '.' {
				decimalCount++
				if decimalCount > 1 {
					return &ValidationError{Field: field, Message: "invalid amount format"}
				}
				if i == 0 || i == len(value)-1 {
					return &ValidationError{Field: field, Message: "invalid amount format"}
				}
				continue
			}
			if c < '0' || c > '9' {
				return &ValidationError{Field: field, Message: "invalid amount format"}
			}
			if c != '0' {
				hasNonZero = true
			}
		}
		if !hasNonZero {
			return &ValidationError{Field: field, Message: "amount must be greater than zero"}
		}
		return nil
	}
}
