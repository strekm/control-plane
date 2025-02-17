package command

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	mothership "github.com/kyma-project/control-plane/components/reconciler/pkg"
	"github.com/kyma-project/control-plane/tools/cli/pkg/logger"
	"github.com/kyma-project/control-plane/tools/cli/pkg/printer"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

type ReconciliationOperationInfoCommand struct {
	ctx          context.Context
	log          logger.Logger
	output       string
	schedulingID string

	provideMshipClient mothershipClientProvider
}

type ReconcilerInfoResponses struct {
	mothership.ReconciliationInfoOKResponse
	KymaConfig mothership.KymaConfig `json:"kymaConfig"`
}

func (cmd *ReconciliationOperationInfoCommand) Validate() error {
	err := ValidateOutputOpt(cmd.output)
	if err != nil {
		return err
	}

	if cmd.schedulingID == "" {
		return errors.New("scheduling-id must not be empty")
	}

	return nil
}

func (cmd *ReconciliationOperationInfoCommand) printReconciliation(data ReconcilerInfoResponses) error {
	switch {
	case cmd.output == tableOutput:
		tp, err := printer.NewTablePrinter([]printer.Column{
			{
				Header:    "COMPONENT",
				FieldSpec: "{.component}",
			},
			{
				Header:    "CORRELATION_ID",
				FieldSpec: "{.correlationID}",
			},
			{
				Header:    "SCHEDULING_ID",
				FieldSpec: "{.schedulingID}",
			},
			{
				Header:    "PRIORITY",
				FieldSpec: "{.priority}",
			},
			{
				Header:    "STATE",
				FieldSpec: "{.state}",
			},
			{
				Header:         "CREATED AT",
				FieldSpec:      "{.created}",
				FieldFormatter: reconciliationOperationCreated,
			},
			{
				Header:         "UPDATED",
				FieldSpec:      "{.updated}",
				FieldFormatter: reconciliationOperationUpdated,
			},
			{
				Header:    "REASON",
				FieldSpec: "{.reason}",
			},
		}, false)
		if err != nil {
			return err
		}

		return tp.PrintObj(data.Operations)
	case cmd.output == jsonOutput:
		jp := printer.NewJSONPrinter("  ")
		jp.PrintObj(data)
	case strings.HasPrefix(cmd.output, customOutput):
		_, templateFile := printer.ParseOutputToTemplateTypeAndElement(cmd.output)
		column, err := printer.ParseColumnToHeaderAndFieldSpec(templateFile)
		if err != nil {
			return err
		}

		ccp, err := printer.NewTablePrinter(column, false)
		if err != nil {
			return err
		}
		return ccp.PrintObj(data)
	}
	return nil
}

func reconciliationOperationCreated(obj interface{}) string {
	sr := obj.(mothership.Operation)
	return sr.Created.Format("2006/01/02 15:04:05")

}
func reconciliationOperationUpdated(obj interface{}) string {
	sr := obj.(mothership.Operation)
	return sr.Updated.Format("2006/01/02 15:04:05")
}

func (cmd *ReconciliationOperationInfoCommand) Run() error {
	cmd.log = logger.New()

	ctx, cancel := context.WithCancel(cmd.ctx)
	defer cancel()

	client, err := cmd.initClient(ctx)
	if err != nil {
		return errors.Wrap(err, "while creating mothership client")
	}

	var result ReconcilerInfoResponses

	recinfo, err := cmd.getReconciliationInfo(ctx, client)
	if err != nil {
		return errors.Wrap(err, "wile fetching reconciliation operation info")
	}

	kymaConfig, err := cmd.getKymaConfigVersion(ctx, client, recinfo)
	if err != nil {
		return errors.Wrap(err, "wile fetching cluster configuration")
	}

	result.ReconciliationInfoOKResponse = recinfo
	result.KymaConfig = kymaConfig

	err = cmd.printReconciliation(result)
	if err != nil {
		return errors.Wrap(err, "while printing runtimes")
	}

	return nil
}

// NewUpgradeCmd constructs the reconciliation command and all subcommands under the reconciliation command
func NewReconciliationOperationInfoCmd() *cobra.Command {
	return newReconciliationOperationInfoCmd(defaultMothershipClientProvider)
}

func newReconciliationOperationInfoCmd(mp mothershipClientProvider) *cobra.Command {
	cmd := ReconciliationOperationInfoCommand{
		provideMshipClient: mp,
	}

	cobraCmd := &cobra.Command{
		Use:     "info",
		Aliases: []string{"i"},
		Short:   "Displays Kyma Reconciliations Information.",
		Long:    `Displays Kyma Reconciliations Information and their primary attributes, such as component, correlation-id or priority.`,
		PreRunE: func(_ *cobra.Command, _ []string) error { return cmd.Validate() },
		RunE:    func(_ *cobra.Command, _ []string) error { return cmd.Run() },
	}

	SetOutputOpt(cobraCmd, &cmd.output)

	cobraCmd.Flags().StringVarP(&cmd.schedulingID, "scheduling-id", "i", "", "Scheduling ID of the specific Kyma Reconciliation.")

	if cobraCmd.Parent() != nil && cobraCmd.Parent().Context() != nil {
		cmd.ctx = cobraCmd.Parent().Context()
		return cobraCmd
	}

	cmd.ctx = context.Background()
	return cobraCmd
}

func (cmd *ReconciliationOperationInfoCommand) initClient(ctx context.Context) (mothership.ClientInterface, error) {
	// fetch reconciliations
	auth := CLICredentialManager(cmd.log)
	httpClient := oauth2.NewClient(ctx, auth)
	mothershipURL := GlobalOpts.MothershipAPIURL()

	return cmd.provideMshipClient(mothershipURL, httpClient)
}

func (cmd *ReconciliationOperationInfoCommand) getReconciliationInfo(ctx context.Context, client mothership.ClientInterface) (mothership.ReconciliationInfoOKResponse, error) {
	response, err := client.GetReconciliationsSchedulingIDInfo(ctx, cmd.schedulingID)
	if err != nil {
		return mothership.ReconciliationInfoOKResponse{}, errors.Wrap(err, "wile fetching reconciliation operation info")
	}

	defer response.Body.Close()

	if isErrResponse(response.StatusCode) {
		err := responseErr(response)
		return mothership.ReconciliationInfoOKResponse{}, err
	}

	var result mothership.ReconciliationInfoOKResponse

	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		return mothership.ReconciliationInfoOKResponse{}, errors.WithStack(ErrMothershipResponse)
	}
	return result, nil
}

func (cmd *ReconciliationOperationInfoCommand) getKymaConfigVersion(ctx context.Context, client mothership.ClientInterface, recInfo mothership.ReconciliationInfoOKResponse) (mothership.KymaConfig, error) {

	response, err := client.GetClustersRuntimeIDConfigVersion(ctx,
		recInfo.RuntimeID,
		strconv.FormatInt(recInfo.ConfigVersion, 10),
	)
	if err != nil {
		return mothership.KymaConfig{}, errors.Wrap(err, "wile fetching cluster configuration")
	}

	defer response.Body.Close()

	if isErrResponse(response.StatusCode) {
		err := responseErr(response)
		return mothership.KymaConfig{}, err
	}

	var result mothership.KymaConfig
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return mothership.KymaConfig{}, errors.WithStack(ErrMothershipResponse)
	}
	return result, nil
}
