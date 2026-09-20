package nodev1

import (
	"sort"
	"testing"

	"google.golang.org/grpc"
)

// upstreamWireContract is every RPC the upstream Rebecca-node binary serves,
// taken from its proto/rebecca/node/v1/node.proto. The panel only talks to a
// node if these exact service and method names line up, so renaming the
// package (as an early rebrand did) silently breaks every node connection.
var upstreamWireContract = map[string][]string{
	"rebecca.node.v1.NodeControlService": {
		"Hello",
		"Connect",
		"Health",
	},
	"rebecca.node.v1.NodeRuntimeService": {
		"StartRuntime",
		"RestartRuntime",
		"StopRuntime",
		"SyncConfig",
		"AddUser",
		"UpdateUser",
		"RemoveUser",
		"Metrics",
		"PublicIPs",
		"TestOutbound",
		"TestRoute",
		"UpdateRuntime",
		"UpdateGeo",
		"RestartService",
		"UpdateService",
		"RebootHost",
		"ApplyIPBlocks",
		"ApplyTorProxy",
		"ConfigureWindscribe",
		"ConfigurePsiphon",
	},
	"rebecca.node.v1.NodeUsageService": {
		"CollectOnlineUsers",
		"CollectUserUsage",
		"AckUserUsage",
		"CollectOutboundUsage",
		"AckOutboundUsage",
	},
	"rebecca.node.v1.NodeLogsService": {
		"StreamLogs",
	},
}

func TestGeneratedServicesMatchTheUpstreamNodeWireContract(t *testing.T) {
	descs := []struct {
		name    string
		methods []string
	}{
		{NodeControlService_ServiceDesc.ServiceName, methodNames(NodeControlService_ServiceDesc.Methods)},
		{NodeRuntimeService_ServiceDesc.ServiceName, methodNames(NodeRuntimeService_ServiceDesc.Methods)},
		{NodeUsageService_ServiceDesc.ServiceName, methodNames(NodeUsageService_ServiceDesc.Methods)},
		{NodeLogsService_ServiceDesc.ServiceName, streamNames(NodeLogsService_ServiceDesc.Streams, NodeLogsService_ServiceDesc.Methods)},
	}
	got := map[string][]string{}
	for _, desc := range descs {
		got[desc.name] = desc.methods
	}
	if len(got) != len(upstreamWireContract) {
		t.Fatalf("expected %d services, generated %d: %v", len(upstreamWireContract), len(got), got)
	}
	for service, want := range upstreamWireContract {
		methods, ok := got[service]
		if !ok {
			t.Errorf("service %q is not served under the upstream name; generated: %v", service, got)
			continue
		}
		sort.Strings(methods)
		wantSorted := append([]string(nil), want...)
		sort.Strings(wantSorted)
		if len(methods) != len(wantSorted) {
			t.Errorf("%s: generated %d methods %v, upstream serves %d %v", service, len(methods), methods, len(wantSorted), wantSorted)
			continue
		}
		for i := range methods {
			if methods[i] != wantSorted[i] {
				t.Errorf("%s: method mismatch at %d: generated %q, upstream %q", service, i, methods[i], wantSorted[i])
			}
		}
	}
}

func TestFullMethodNamesUseTheUpstreamPackage(t *testing.T) {
	for _, name := range []string{
		NodeControlService_Hello_FullMethodName,
		NodeRuntimeService_SyncConfig_FullMethodName,
		NodeRuntimeService_AddUser_FullMethodName,
		NodeUsageService_CollectUserUsage_FullMethodName,
	} {
		if len(name) < len("/rebecca.node.v1.") || name[:len("/rebecca.node.v1.")] != "/rebecca.node.v1." {
			t.Errorf("wire path %q must live under /rebecca.node.v1. to reach the upstream node", name)
		}
	}
}
func methodNames(methods []grpc.MethodDesc) []string {
	names := make([]string, 0, len(methods))
	for _, method := range methods {
		names = append(names, method.MethodName)
	}
	return names
}

// streamNames lists unary and streaming RPCs together, since a service such as
// NodeLogsService is made of streams.
func streamNames(streams []grpc.StreamDesc, methods []grpc.MethodDesc) []string {
	names := methodNames(methods)
	for _, stream := range streams {
		names = append(names, stream.StreamName)
	}
	return names
}
