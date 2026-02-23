// SPDX-License-Identifier: Apache-2.0

package domain

import "errors"

var ErrMaxConcurrentRunsExceeded = errors.New("max concurrent runs exceeded")
var ErrWorkflowTemplateNotFound = errors.New("workflow template not found")
var ErrInvalidAPIKeyName = errors.New("invalid api key name")
