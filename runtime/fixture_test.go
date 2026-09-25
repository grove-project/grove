package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/testapp"
	"go.yaml.in/yaml/v3"
)

const artifactManifest = `{"format_version":1,"application_id":"` + testapp.ApplicationID + `","code_version":"v0.1.0-dev","components":[{"service_id":1,"name":"Orders","runtime":"process","entrypoint":["worker","--component","orders"]},{"service_id":2,"name":"Inventory","runtime":"process","entrypoint":["worker","--component","inventory"]},{"service_id":3,"name":"Payment","runtime":"process","entrypoint":["worker","--component","payment"]},{"service_id":4,"name":"Shipping","runtime":"process","entrypoint":["worker","--component","shipping"]},{"service_id":5,"name":"Web","runtime":"process","entrypoint":["worker","--component","web"]}],"ui_assets":["web/index.html"],"config_region":{"format_version":1,"capacity":4096}}`

var embeddedArtifact = ArtifactManifestPrefix + artifactManifest + ArtifactManifestSuffix +
	ArtifactConfigPrefix + BlankConfigRegion + ArtifactConfigSuffix

func init() {
	if err := configureApplication(testRuntimeDefinition()); err != nil {
		panic(err)
	}
}

// RuntimeDefinition composes Grove test app with Grove's public application
// runtime. All business implementations, IDs, configuration, Web behavior,
// and application actions remain owned by this package.
func testRuntimeDefinition() Definition {
	return Definition{
		Name:             "Grove Test App",
		ApplicationID:    testapp.ApplicationID,
		EmbeddedArtifact: &embeddedArtifact,
		Configuration: ConfigurationProvider{
			Default: runtimeDefaultConfiguration,
			Compile: runtimeCompileConfiguration,
			Decode:  runtimeDecodeConfiguration,
			Failure: runtimeConfigurationFailure,
		},
		Components: []Component{
			{ServiceID: testapp.ServiceOrders, Name: "Orders", Kind: "orders", Register: registerRuntimeOrders},
			{ServiceID: testapp.ServiceInventory, Name: "Inventory", Kind: "inventory", Register: registerRuntimeInventory},
			{ServiceID: testapp.ServicePayment, Name: "Payment", Kind: "payment", Register: registerRuntimePayment},
			{ServiceID: testapp.ServiceShipping, Name: "Shipping", Kind: "shipping", Register: registerRuntimeShipping},
			{ServiceID: testapp.ServiceWeb, Name: "Web", Kind: "web", HTTPHandler: runtimeWebHandler},
		},
		RegisterActions: testapp.RegisterActions,
		IntegrityAction: testapp.ActionVerifyOrders,
		Scenario: &Scenario{
			NodeCount:      3,
			DebugNodeCount: 5,
			InitialPlacements: []ScenarioPlacement{
				{ServiceID: testapp.ServiceOrders, NodeID: "node-1"},
				{ServiceID: testapp.ServiceInventory, NodeID: "node-2"},
				{ServiceID: testapp.ServiceWeb, NodeID: "node-1"},
			},
			StartupComponents: []ScenarioPlacement{
				{ServiceID: testapp.ServiceOrders, NodeID: "node-1", Options: []string{"distributed"}},
				{ServiceID: testapp.ServiceInventory, NodeID: "node-1"},
				{ServiceID: testapp.ServicePayment, NodeID: "node-1"},
				{ServiceID: testapp.ServiceShipping, NodeID: "node-1"},
				{ServiceID: testapp.ServiceWeb, NodeID: "node-1"},
				{ServiceID: testapp.ServiceInventory, NodeID: "node-2"},
				{ServiceID: testapp.ServiceShipping, NodeID: "node-2"},
				{ServiceID: testapp.ServicePayment, NodeID: "node-3"},
			},
			DebugPlacements: []ScenarioPlacement{
				{ServiceID: testapp.ServiceWeb, NodeID: "node-1"},
				{ServiceID: testapp.ServiceOrders, NodeID: "node-2", Options: []string{"distributed"}},
				{ServiceID: testapp.ServiceInventory, NodeID: "node-3"},
				{ServiceID: testapp.ServicePayment, NodeID: "node-4"},
				{ServiceID: testapp.ServiceShipping, NodeID: "node-5"},
			},
			RecoveryServiceID: testapp.ServiceInventory,
			Probe:             runtimeOrderProbe,
			ProbeHealthy:      runtimeOrderHealthy,
			ProbeSummary:      runtimeOrderSummary,
			InvalidConfig:     runtimeInvalidConfiguration,
		},
	}
}

func runtimeDefaultConfiguration() (Configuration, error) {
	configuration := testapp.DefaultConfiguration()
	payload, err := testapp.EncodeConfiguration(configuration)
	if err != nil {
		return Configuration{}, err
	}
	return runtimeConfiguration(configuration, payload, nil), nil
}

func runtimeCompileConfiguration(source []byte) (Configuration, error) {
	configuration, canonical, err := testapp.CompileConfigurationYAML(source)
	if err != nil {
		return Configuration{}, err
	}
	payload, err := testapp.EncodeConfiguration(configuration)
	if err != nil {
		return Configuration{}, err
	}
	return runtimeConfiguration(configuration, payload, canonical), nil
}

func runtimeDecodeConfiguration(payload []byte) (Configuration, error) {
	configuration, err := testapp.DecodeConfiguration(payload)
	if err != nil {
		return Configuration{}, err
	}
	return runtimeConfiguration(configuration, payload, nil), nil
}

func runtimeConfiguration(configuration testapp.Configuration, payload, canonical []byte) Configuration {
	return Configuration{
		Value:         configuration,
		Revision:      configuration.Revision,
		Encoding:      "gob",
		Payload:       payload,
		CanonicalYAML: canonical,
		Facts: map[string]string{
			"cluster.name": configuration.Cluster.Name,
			"node.zone":    configuration.Node.Zone,
		},
	}
}

func runtimeConfigurationFailure(err error) (string, string) {
	var validation *testapp.ConfigurationError
	if errors.As(err, &validation) {
		return validation.Field, validation.Message
	}
	return "", err.Error()
}

func registerRuntimeOrders(ctx ComponentContext) error {
	var orders *testapp.Orders
	if ctx.Client == nil {
		orders = testapp.NewOrders(&testapp.Inventory{}, &testapp.Payment{}, &testapp.Shipping{})
	} else if slices.Contains(ctx.Options, "distributed") {
		orders = testapp.NewDistributedOrders(ctx.Client)
	} else {
		orders = testapp.NewGroveOrders(ctx.Client, &testapp.Payment{}, &testapp.Shipping{})
	}
	return testapp.RegisterOrders(ctx.Registry, orders)
}

func registerRuntimeInventory(ctx ComponentContext) error {
	configuration, ok := ctx.Configuration.(testapp.Configuration)
	if !ok {
		return errors.New("Grove test app runtime configuration has an unexpected type")
	}
	return testapp.RegisterInventory(ctx.Registry, testapp.NewInventory(configuration.Inventory.ReservationBuffer))
}

func registerRuntimePayment(ctx ComponentContext) error {
	return testapp.RegisterPayment(ctx.Registry, &testapp.Payment{})
}

func registerRuntimeShipping(ctx ComponentContext) error {
	return testapp.RegisterShipping(ctx.Registry, &testapp.Shipping{})
}

func runtimeWebHandler(ctx ComponentContext) (http.Handler, error) {
	configuration, ok := ctx.Configuration.(testapp.Configuration)
	if !ok {
		return nil, errors.New("Grove test app runtime configuration has an unexpected type")
	}
	readStatus := func(callCtx context.Context) (testapp.ClusterStatusView, error) {
		status, err := ctx.ReadStatus(callCtx)
		if err != nil {
			return testapp.ClusterStatusView{}, err
		}
		return groveShopStatus(status), nil
	}
	createOrder := func(callCtx context.Context, request testapp.CreateOrderRequest) (testapp.Order, error) {
		return grove.Call[testapp.CreateOrderRequest, testapp.Order](callCtx, ctx.Client, testapp.ServiceOrders, testapp.MethodCreateOrder, request)
	}
	return testapp.WebHandlerWithRuntime(configuration, ctx.ConfigDigest, readStatus, createOrder), nil
}

func groveShopStatus(status ClusterStatus) testapp.ClusterStatusView {
	view := testapp.ClusterStatusView{Health: status.Health, Ready: status.Ready}
	view.Nodes = make([]testapp.NodeStatusView, 0, len(status.Nodes))
	for _, node := range status.Nodes {
		converted := testapp.NodeStatusView{NodeID: node.NodeID, Health: node.Health, Error: node.Error}
		for _, component := range node.Components {
			converted.Components = append(converted.Components, testapp.ComponentStatusView{
				ServiceID: component.ServiceID, Name: component.Name, WorkerID: component.WorkerID,
				State: component.State, Error: component.Error,
			})
		}
		view.Nodes = append(view.Nodes, converted)
	}
	for _, placement := range status.Placements {
		view.Placements = append(view.Placements, testapp.PlacementStatusView{
			ServiceID: placement.ServiceID, Name: placement.Name, NodeID: placement.NodeID,
			InvocationSubject: placement.InvocationSubject, ArtifactDigest: placement.ArtifactDigest, Health: placement.Health,
		})
	}
	view.ActiveArtifact = groveShopArtifactStatus(status.ActiveArtifact)
	view.CandidateArtifact = groveShopArtifactStatus(status.CandidateArtifact)
	if status.Rollout != nil {
		view.Rollout = &testapp.RolloutStatusView{Generation: status.Rollout.Generation, Phase: status.Rollout.Phase}
		if status.Rollout.Failure != nil {
			view.Rollout.Failure = &testapp.RolloutFailureView{
				Code: status.Rollout.Failure.Code, Component: status.Rollout.Failure.Component,
				Field: status.Rollout.Failure.Field, Message: status.Rollout.Failure.Message,
			}
		}
	}
	return view
}

func groveShopArtifactStatus(status *ArtifactStatus) *testapp.ArtifactStatusView {
	if status == nil {
		return nil
	}
	return &testapp.ArtifactStatusView{
		ApplicationID: status.ApplicationID, CodeVersion: status.CodeVersion,
		ArtifactDigest: status.ArtifactDigest, ConfigRevision: status.ConfigRevision, ConfigDigest: status.ConfigDigest,
	}
}

func runtimeOrderProbe(ctx context.Context, webAddress, orderID string) (any, error) {
	requestBody, err := json.Marshal(testapp.CreateOrderRequest{
		OrderID: orderID, SKU: "coffee-beans", Quantity: 1,
		AmountCents: 1200, ShippingAddress: "31 Grove Lane",
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+webAddress+"/api/orders", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("POST /api/orders: %s: %s", response.Status, body)
	}
	var order testapp.Order
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		return nil, err
	}
	return order, nil
}

func runtimeOrderHealthy(value any) bool {
	order, ok := value.(testapp.Order)
	return ok && order.Status == testapp.OrderCompleted && slices.Equal(order.History, []testapp.OrderStatus{
		testapp.OrderCreated, testapp.OrderReserved, testapp.OrderPaid, testapp.OrderShipping, testapp.OrderCompleted,
	})
}

func runtimeOrderSummary(value any) string {
	if order, ok := value.(testapp.Order); ok {
		return string(order.Status)
	}
	return fmt.Sprintf("%v", value)
}

func runtimeInvalidConfiguration(source []byte) (Configuration, string, string, error) {
	configuration := testapp.DefaultConfiguration()
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)
	if err := decoder.Decode(&configuration); err != nil {
		return Configuration{}, "", "", fmt.Errorf("decode candidate configuration: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Configuration{}, "", "", errors.New("candidate configuration contains multiple YAML documents")
	}
	validationErr := testapp.ValidateConfiguration(configuration)
	var validation *testapp.ConfigurationError
	if validationErr == nil || !errors.As(validationErr, &validation) {
		return Configuration{}, "", "", errors.New("candidate configuration must violate Grove test app validation")
	}
	payload, err := grove.Encode(configuration)
	if err != nil {
		return Configuration{}, "", "", fmt.Errorf("encode invalid candidate configuration: %w", err)
	}
	return runtimeConfiguration(configuration, payload, source), validation.Field, validation.Message, nil
}

func createApplicationOrder(ctx context.Context, webAddress, orderID string) (testapp.Order, error) {
	value, err := runtimeOrderProbe(ctx, webAddress, orderID)
	if err != nil {
		return testapp.Order{}, err
	}
	return value.(testapp.Order), nil
}

func applicationOrderCompleted(order testapp.Order) bool {
	return runtimeOrderHealthy(order)
}

func waitForApplicationOrder(ctx context.Context, webAddress, orderID string) (testapp.Order, error) {
	value, err := waitForApplicationProbe(ctx, webAddress, orderID)
	if err != nil {
		return testapp.Order{}, err
	}
	return value.(testapp.Order), nil
}
