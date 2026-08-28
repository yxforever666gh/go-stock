package themes

import "fmt"

var lifecycleOrder = map[LifecycleStage]int{
	StageObserve: 0, StageFerment: 1, StageAccelerate: 2, StageDiverge: 3, StageFade: 4,
}

func IsValidStage(stage LifecycleStage) bool {
	_, ok := lifecycleOrder[stage]
	return ok
}

func ValidateLifecycle(previous *DailySnapshot, cycleNo int, stage LifecycleStage) error {
	if cycleNo < 1 || !IsValidStage(stage) {
		return fmt.Errorf("%w: invalid cycle or stage", ErrInvalidLifecycle)
	}
	if previous == nil {
		if stage != StageObserve {
			return fmt.Errorf("%w: first cycle must start at %s", ErrInvalidLifecycle, StageObserve)
		}
		return nil
	}
	if cycleNo == previous.CycleNo {
		oldIndex, newIndex := lifecycleOrder[previous.LifecycleStage], lifecycleOrder[stage]
		if newIndex == oldIndex || newIndex == oldIndex+1 {
			return nil
		}
		return fmt.Errorf("%w: cycle %d can only hold or advance one stage", ErrInvalidLifecycle, cycleNo)
	}
	if cycleNo == previous.CycleNo+1 && previous.LifecycleStage == StageFade && stage == StageObserve {
		return nil
	}
	return fmt.Errorf("%w: a new cycle may start at %s only after %s", ErrInvalidLifecycle, StageObserve, StageFade)
}
