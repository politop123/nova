package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlannerTaskCapabilities(t *testing.T) {
	seen := map[string]Capability{}
	for _, capability := range PlannerCapabilities() {
		seen[capability.ActionType] = capability
	}
	for _, action := range []string{"task.complete", "task.cancel", "task.reschedule"} {
		capability, ok := seen[action]
		if !ok || capability.Intent != action || !strings.Contains(ActionPlannerProfile, action) {
			t.Fatalf("task action not taught: %s", action)
		}
	}
	input := PlannerInput{UserText: "Рахунок уже оплатив", OpenTasks: []PlannerTask{{Title: "Оплатити рахунок"}}, TasksTruncated: true}
	var decoded PlannerInput
	if err := json.Unmarshal([]byte(BuildPlannerInput(input)), &decoded); err != nil || len(decoded.OpenTasks) != 1 || !decoded.TasksTruncated {
		t.Fatalf("preview not serialized: %+v %v", decoded, err)
	}
}
