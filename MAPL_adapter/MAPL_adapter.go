// nolint:lll
// Generates the MAPL_adapter adapter's resource yaml. It contains the adapter's configuration, name,
// supported template names (authorization in this case), and whether it is session or no-session based.
//go:generate $GOPATH/src/istio.io/istio/bin/mixer_codegen.sh -a mixer/adapter/MAPL_adapter/config/config.proto -x "-s=false -n mapl-adapter -t authorization"

// Package MAPL_adapter contains the gRPC adapter for Istio's mixer
package MAPL_adapter

import (
	"context"
	"fmt"
	"net"
	"time"
	"log"
	"google.golang.org/grpc"

	"istio.io/api/mixer/adapter/model/v1beta1"
	"istio.io/istio/mixer/adapter/MAPL_adapter/config"
	"istio.io/istio/mixer/template/authorization"

	"github.com/gogo/googleapis/google/rpc"

	"github.com/octarinesec/MAPL/MAPL_engine"

	"io/ioutil"
)

// Server is basic server interface
type (
	Server interface {
		Addr() string
		Close() error
		Run(shutdown chan error)
	}

	// MaplAdapter supports authorization templates
	MaplAdapter struct {
		listener net.Listener
		server   *grpc.Server
		rules MAPL_engine.Rules
	}
)

var _ authorization.HandleAuthorizationServiceServer = &MaplAdapter{}

// HandleAuthorization is the main gRPC function that is called by Istio's Mixer and checks the message attributes against the rules.
func (s *MaplAdapter) HandleAuthorization(ctx context.Context, authRequest *authorization.HandleAuthorizationRequest) (*v1beta1.CheckResult, error) {

	//log.Println("received request %v\n", *authRequest)

	cfg := &config.Params{}

	if authRequest.AdapterConfig != nil {
		if err := cfg.Unmarshal(authRequest.AdapterConfig.Value); err != nil {
			log.Println("error unmarshalling adapter config:", err)
			return nil, err
		}
	}

	message := convertAuthRequestToMaplMessage(authRequest)  // convert authRequest (from the mixer) to message attributes as in the definitions.go file.
	maplCode, _, _, _, _:= MAPL_engine.Check(&message, &s.rules)  // check the message against the rules with the MAPL_engine's Check function.
	statusCode,statusMsg:=convertDecisionToIstioCode(maplCode) // convert MAPL_engine's decision to Istio's status code.

	//log.Println("logger",Params.Logger)

	log.Printf("Check result: %d [%d]\n", statusCode, maplCode)

	status := rpc.Status{
		Code:    statusCode,
		Message: statusMsg,
	}

	result := &v1beta1.CheckResult{
		Status:        status,
		ValidDuration: time.Duration(time.Duration(Params.CacheTimeoutSecs) * time.Second),
		ValidUseCount: 1000,
	}

	//log.Printf("Sending result: %+v\n", result)

	return result, nil
}

// Addr returns the listening address of the server
func (s *MaplAdapter) Addr() string {
	return s.listener.Addr().String()
}

// Run starts the server run
func (s *MaplAdapter) Run(shutdown chan error) {
	shutdown <- s.server.Serve(s.listener)
}

// Close gracefully shuts down the server; used for testing
func (s *MaplAdapter) Close() error {
	if s.server != nil {
		s.server.GracefulStop()
	}

	if s.listener != nil {
		_ = s.listener.Close()
	}

	return nil
}

//IstioToServicenameConvention types
const (
	IstioUid = iota // k8s pod name
	IstioWorkloadAndNamespace
)

var IstioToServicenameConventionString = [...]string{
	IstioUid: "IstioUid",
	IstioWorkloadAndNamespace: "IstioWorkloadAndNamespace",
}

// MaplAdapterParams type contains global parameters
type MaplAdapterParams struct {
	AdapterName string
	CacheTimeoutSecs int
	IstioToServiceNameConvention int
	Logging bool
	RulesFileName string
}

var Params MaplAdapterParams // global parameters

// NewMaplAdapter creates a new IBP adapter that listens at provided port.
func NewMaplAdapter(port string, rulesFilename string) (Server, error) {

	//log.Println(Params)


	if Params.Logging{
		/* in remarks. just output to stdout and use kubectl logs...
		// setup a log outfile file
		f, err := os.OpenFile("log.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0777)
		if err != nil {
			log.Println(err)
		}
		defer f.Close()
		log.SetOutput(f) //set output of logs to f
		*/
	}else{
		log.SetOutput(ioutil.Discard) // output discarded if no log is needed
	}
	// -------------------------

	if port == "" {
		port = "0"
	}
	listener, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		return nil, fmt.Errorf("unable to listen on socket: %v", err)
	}
	s := &MaplAdapter{
		listener: listener,
		rules: MAPL_engine.YamlReadRulesFromFile(rulesFilename),
	}
	log.Printf("read %v rules from file \"%v\"\n",len(s.rules.Rules),rulesFilename)
	log.Printf("listening on \"%v\"\n", s.Addr())
	s.server = grpc.NewServer()
	authorization.RegisterHandleAuthorizationServiceServer(s.server, s)

	return s, nil
}


// convertAuthRequestToMaplMessage converts authRequest (from Istio's Mixer) to MAPL_engine.MessageAttributes as defined in definitions.go.
func convertAuthRequestToMaplMessage(authRequest *authorization.HandleAuthorizationRequest) MAPL_engine.MessageAttributes{
	instance := authRequest.Instance
	log.Println("-----------------------")
	if true { // for debugging:
		logInstance(authRequest)
	}

	var message MAPL_engine.MessageAttributes

	message.RequestMethod = instance.Action.Method
	message.ContextProtocol = instance.Action.Properties["protocol"].GetStringValue()
	message.RequestPath = instance.Action.Path
	MAPL_engine.AddResourceType(&message) // to add message.ContextType

	switch Params.IstioToServiceNameConvention{
	case IstioUid:
		message.SourceService = instance.Subject.Properties["sourceUid"].GetStringValue()
		message.DestinationService = instance.Action.Properties["destinationUid"].GetStringValue()
	case IstioWorkloadAndNamespace:
		message.SourceService = instance.Subject.Properties["sourceWorkloadName"].GetStringValue()+ "." + instance.Subject.Properties["sourceWorkloadNamespace"].GetStringValue()
		message.DestinationService = instance.Action.Properties["destinationWorkloadName"].GetStringValue()+ "." + instance.Action.Properties["destinationWorkloadNamespace"].GetStringValue()
	}

	//log.Println("sourceWorkload",instance.Subject.Properties["sourceWorkloadName"].GetStringValue(),instance.Subject.Properties["sourceWorkloadNamespace"].GetStringValue())
	//log.Println("destinationWorkload",instance.Action.Properties["destinationWorkloadName"].GetStringValue(),instance.Action.Properties["destinationWorkloadNamespace"].GetStringValue())
	//log.Println("message.SourceService:",message.SourceService)
	//log.Println("message.DestinationService:",message.DestinationService)

	//log.Println("logger",Params.Logger)

	log.Printf("messageAttributes: %+v\n",message)
	log.Printf("-----------------------\n")

	return message
}

//convertDecisionToIstioCode converts MAPL_engine's decision to Istio's status code
func convertDecisionToIstioCode(decision int) (int32, string){
	statusCode := int32(0)
	statusMsg := ""

	switch decision {
	case MAPL_engine.DEFAULT:
		statusCode = 16
		statusMsg = "traffic has been blocked by default"
	case MAPL_engine.BLOCK:
		statusCode = 16
		statusMsg = "traffic has been blocked by rule"
	case MAPL_engine.ALERT:
		statusMsg = "traffic has been alerted by rule"
	}
	return statusCode,statusMsg
}

// logInstance output authRequest data to log file (used for debugging)
func logInstance(authRequest *authorization.HandleAuthorizationRequest) {
	instance := authRequest.Instance

	if false {
		log.Println("sourceAddress:", instance.Subject.Properties["sourceAddress"].GetStringValue())
		log.Println("sourceName:", instance.Subject.Properties["sourceName"].GetStringValue())
		log.Println("sourceUid:", instance.Subject.Properties["sourceUid"].GetStringValue())
		log.Println("sourceNamespace:", instance.Subject.Properties["sourceNamespace"].GetStringValue())
		log.Println("sourceVersion:", instance.Subject.Properties["sourceVersion"].GetStringValue())
		log.Println("sourcePrincipal:", instance.Subject.Properties["sourcePrincipal"].GetStringValue())
		log.Println("sourceOwner:", instance.Subject.Properties["sourceOwnern"].GetStringValue())
		log.Println("sourceWorkloadUid:", instance.Subject.Properties["sourceWorkloadUid"].GetStringValue())
		log.Println("sourceWorkloadName:", instance.Subject.Properties["sourceWorkloadName"].GetStringValue())
		log.Println("sourceWorkloadNamespace:", instance.Subject.Properties["sourceWorkloadNamespace"].GetStringValue())

		log.Println("instance.Action.Namespace:", instance.Action.Namespace)
		log.Println("instance.Action.Service:", instance.Action.Service)
		log.Println("instance.Action.Method:", instance.Action.Method)
		log.Println("instance.Action.Path:", instance.Action.Path)

		log.Println("protocol:", instance.Action.Properties["protocol"].GetStringValue())
		log.Println("destinationAddress:", instance.Action.Properties["destinationAddress"].GetStringValue())
		log.Println("destinationName:", instance.Action.Properties["destinationName"].GetStringValue())
		log.Println("destinationUid:", instance.Action.Properties["destinationUid"].GetStringValue())
		log.Println("destinationNamespace:", instance.Action.Properties["destinationNamespace"].GetStringValue())
		log.Println("destinationVersion:", instance.Action.Properties["destinationVersion"].GetStringValue())

		log.Println("destinationWorkloadUid:", instance.Action.Properties["destinationWorkloadUid"].GetStringValue())
		log.Println("destinationWorkloadName:", instance.Action.Properties["destinationWorkloadName"].GetStringValue())
		log.Println("destinationWorkloadNamespace:", instance.Action.Properties["destinationWorkloadNamespace"].GetStringValue())
	}
	log.Println("-------------------------------------------")
	log.Println(instance)
	log.Println("-------------------------------------------")
}