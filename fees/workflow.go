package fees

import (
	"context"
	"encoding/json"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type BillingWorkflowInput struct {
	BillID      string
	WorkflowID  string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Metadata    json.RawMessage
}

type CloseSignal struct {
	Reason string
}

func BillingPeriodWorkflow(ctx workflow.Context, input BillingWorkflowInput) error {
	if err := validateCreateInput(input); err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    5,
		},
	})

	if err := workflow.ExecuteActivity(ctx, CreateBillActivity, input).Get(ctx, nil); err != nil {
		return err
	}

	sleepDuration := input.PeriodEnd.Sub(workflow.Now(ctx))
	if sleepDuration <= 0 {
		return workflow.ExecuteActivity(ctx, CloseBillActivity, input.BillID).Get(ctx, nil)
	}

	signalChan := workflow.GetSignalChannel(ctx, closeSignalName)
	timer := workflow.NewTimer(ctx, sleepDuration)
	selector := workflow.NewSelector(ctx)

	closedBySignal := false
	selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
		var signal CloseSignal
		c.Receive(ctx, &signal)
		closedBySignal = true
	})
	selector.AddFuture(timer, func(f workflow.Future) {})

	selector.Select(ctx)
	if closedBySignal {
		return nil
	}

	return workflow.ExecuteActivity(ctx, CloseBillActivity, input.BillID).Get(ctx, nil)
}

func CreateBillActivity(ctx context.Context, input BillingWorkflowInput) error {
	return insertBillRecord(ctx, input)
}

func CloseBillActivity(ctx context.Context, billID string) error {
	activity.RecordHeartbeat(ctx, billID)
	_, _, err := closeBill(ctx, billID)
	return err
}
