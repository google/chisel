// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package contents

// DeadInfoState holds pseudo-enumerated constants for DeadInfo states.
type DeadInfoState int

// The enumerated values for DeadInfoState.
const (
	DeadStateUnset DeadInfoState = iota
	DeadStateRetired
	DeadStateSucceeded
)

// DeadInfoStateString emulates a constant map of int to string.
func DeadInfoStateString(state DeadInfoState) string {
	switch state {
	case DeadStateUnset:
		return "Unset"
	case DeadStateRetired:
		return "Retired"
	case DeadStateSucceeded:
		return "Succeeded"
	default:
		return ""
	}
}

// OverlapState holds pseudo-enumerated constants for overlap states.
type OverlapState int

// The enumerated values for OverlapState.
const (
	firstFurther OverlapState = iota
	secondFurther
	sameEnd
)

// ShiftDirection holds pseudo-enumerated constants for shift directions.
type ShiftDirection int

// The enumerated values for ShiftDirection.
const (
	initial ShiftDirection = iota
	insertion
	deletion
	offsetting
)

// BoundaryType holds pseudo-enumerated constants for boundary types.
type BoundaryType int

// The enumerated values for Boundary Type.
const (
	start BoundaryType = iota
	end
)
