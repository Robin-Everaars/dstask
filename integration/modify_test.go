package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModifyTasksByID(t *testing.T) {
	repo, cleanup := makeDstaskRepo(t)
	defer cleanup()

	program := testCmd(repo)

	output, exiterr, success := program("add", "one", "+one")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("add", "two", "+two")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("add", "three", "+three")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("modify", "2", "3", "+extra")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("next")
	assertProgramResult(t, output, exiterr, success)

	tasks := unmarshalTaskArray(t, output)
	assert.ElementsMatch(
		t,
		[]string{"three", "extra"},
		tasks[2].Tags,
		"extra tag added to task three",
	)
	assert.ElementsMatch(t, []string{"two", "extra"}, tasks[1].Tags, "extra tag added to task two")
	assert.ElementsMatch(t, []string{"one"}, tasks[0].Tags, "task 1 not modified")
}

func TestModifyTasksInContext(t *testing.T) {
	repo, cleanup := makeDstaskRepo(t)
	defer cleanup()

	program := testCmd(repo)

	output, exiterr, success := program("add", "one", "+one")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("add", "two", "+two")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("add", "three", "+three")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("context", "+three")
	assertProgramResult(t, output, exiterr, success)

	defer setEnv("DSTASK_ALLOW_BULK_MODIFY", "1")()
	output, exiterr, success = program("modify", "+extra")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("next")
	assertProgramResult(t, output, exiterr, success)

	tasks := unmarshalTaskArray(t, output)
	assert.Equal(t, []string{"extra", "three"}, tasks[0].Tags, "tags should have been modified")
}

func TestModifyByUUIDTouchesOnlyThatTask(t *testing.T) {
	repo, cleanup := makeDstaskRepo(t)
	defer cleanup()

	program := testCmd(repo)
	for _, summary := range []string{"one", "two", "three"} {
		output, exiterr, success := program("add", summary)
		assertProgramResult(t, output, exiterr, success)
	}
	output, exiterr, success := program("show-open")
	assertProgramResult(t, output, exiterr, success)
	target := unmarshalTaskArray(t, output)[1].UUID

	output, exiterr, success = program(target, "modify", "P1", "+picked")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("show-open")
	assertProgramResult(t, output, exiterr, success)
	for _, task := range unmarshalTaskArray(t, output) {
		if task.UUID == target {
			assert.Equal(t, "P1", task.Priority, "the UUID target is modified")
			assert.Equal(t, []string{"picked"}, task.Tags, "the UUID target is tagged")
		} else {
			assert.Equal(t, "P2", task.Priority, "task %q must not be modified", task.Summary)
			assert.Empty(t, task.Tags, "task %q must not be tagged", task.Summary)
		}
	}
}

func TestModifyWithoutTargetRefusesWithoutTerminal(t *testing.T) {
	repo, cleanup := makeDstaskRepo(t)
	defer cleanup()

	program := testCmd(repo)
	for _, summary := range []string{"one", "two"} {
		output, exiterr, success := program("add", summary)
		assertProgramResult(t, output, exiterr, success)
	}

	_, exiterr, success := program("modify", "P1")
	assert.False(t, success, "a bulk modify without a terminal to confirm must fail")
	if exiterr != nil {
		assert.Contains(t, string(exiterr.Stderr), "DSTASK_ALLOW_BULK_MODIFY")
	}

	output, exiterr, success := program("show-open")
	assertProgramResult(t, output, exiterr, success)
	for _, task := range unmarshalTaskArray(t, output) {
		assert.Equal(t, "P2", task.Priority, "task %q must not be modified", task.Summary)
	}
}

func TestModifyByUnknownUUIDFails(t *testing.T) {
	repo, cleanup := makeDstaskRepo(t)
	defer cleanup()

	program := testCmd(repo)
	output, exiterr, success := program("add", "one")
	assertProgramResult(t, output, exiterr, success)

	_, _, success = program("0b8a8f5e-8c5e-4a8e-9b1e-5f1d2c3b4a59", "modify", "P1")
	assert.False(t, success, "an unknown UUID must not fall back to a bulk modify")

	output, exiterr, success = program("show-open")
	assertProgramResult(t, output, exiterr, success)
	assert.Equal(t, "P2", unmarshalTaskArray(t, output)[0].Priority)
}
