# Deploying the llm-katan Simulator on OpenShift

This guide deploys the [llm-katan](https://github.com/opendatahub-io/llm-katan) LLM provider simulator into an OpenShift cluster with the MaaS (Models as a Service) platform and IPP (Inference Payload Processor) pipeline. The simulator runs in echo mode (no GPU, no model download) and provides mock endpoints for OpenAI, Anthropic, Azure OpenAI, Bedrock, and Vertex AI.

## Prerequisites

- `oc` CLI logged in to the target cluster
- `helm` v3 installed
- MaaS platform deployed (includes the ExternalModel CRD and controller)
- An Istio-based Gateway API Gateway already deployed (e.g. `maas-default-gateway`)
- The repo cloned locally:
  ```bash
  git clone https://github.com/opendatahub-io/ai-gateway-payload-processing.git
  cd ai-gateway-payload-processing
  ```

## Step 1: Deploy IPP (Inference Payload Processor)

IPP must be installed **in the same namespace as the Gateway** (required for Istio EnvoyFilter `targetRefs`). Set the gateway name to match your cluster:

```bash
export GATEWAY_NAME=maas-default-gateway
export GATEWAY_NAMESPACE=openshift-ingress
```

Install:
```bash
helm install payload-processing ./deploy/payload-processing \
  --namespace ${GATEWAY_NAMESPACE} \
  --dependency-update \
  --set upstreamBbr.inferenceGateway.name=${GATEWAY_NAME} \
  --set upstreamBbr.provider.istio.envoyFilter.operation=INSERT_FIRST
```

Disable Istio sidecar injection on the IPP pod (ext_proc uses self-signed TLS, which the sidecar intercepts and breaks):

```bash
oc patch deployment payload-processing -n ${GATEWAY_NAMESPACE} --type=merge \
  -p='{"spec":{"template":{"metadata":{"annotations":{"sidecar.istio.io/inject":"false"}}}}}'
```

Wait for rollout:
```bash
oc rollout status deployment/payload-processing -n ${GATEWAY_NAMESPACE} --timeout=120s
```

## Step 2: Deploy the llm-katan Simulator

The chart must be deployed using `helm template` piped into `kubectl apply --server-side --force-conflicts` because the MaaS ExternalModel controller creates resources (HTTPRoutes, Services, ServiceEntries) from ExternalModel CRs, which causes field-ownership conflicts with `helm install`:

```bash
helm template sim ./deploy/llm-katan-simulator --skip-crds \
  | kubectl apply --server-side --force-conflicts -f -
```

This creates a `llm-katan` namespace containing:

| Resource | Count | Purpose |
|----------|-------|---------|
| Namespace | 1 | `llm-katan` with `istio-injection: enabled` |
| Deployment | 1 | llm-katan simulator pod (echo mode, TLS) |
| Service (ClusterIP) | 1 | Internal endpoint for the simulator |
| ExternalModel CRs | 5 | One per provider, with `maas.opendatahub.io/tls: "false"` annotation |
| MaaSModelRef CRs | 5 | Registers each ExternalModel in the MaaS catalog |
| MaaSAuthPolicy | 1 | Grants access to `system:authenticated` |
| MaaSSubscription | 1 | Token rate limits for all sim models |
| Secrets | 5 | Test API keys (labeled `bbr-managed`) |
| Route | 1 | OpenShift route for the simulator dashboard UI |

The ExternalModel controller automatically creates per-model HTTPRoutes, ExternalName Services, and ServiceEntries — these are not part of the chart.

The `maas.opendatahub.io/tls: "false"` annotation on ExternalModels prevents the controller from creating DestinationRules. TLS origination to the simulator is handled by the platform's wildcard DestinationRule (`data-science-tls-rule`) which includes `insecureSkipVerify: true` for the simulator's self-signed serving certificate.

Wait for the simulator to start (first boot runs `pip install`, takes ~60s):
```bash
oc rollout status deployment/sim -n llm-katan --timeout=180s
```

Check logs:
```bash
oc logs -n llm-katan deploy/sim --tail=20
```

You should see:
```
LLM Katan ready on 0.0.0.0:8443
```

## Step 3: Create an API Key

The MaaS platform requires an API key for inference requests. Create one using the MaaS API:

```bash
export GW_HOST=$(oc get gateway ${GATEWAY_NAME} -n ${GATEWAY_NAMESPACE} \
  -o jsonpath='{.spec.listeners[0].hostname}')
TOKEN=$(oc whoami -t)

API_KEY=$(curl -sS -X POST "http://${GW_HOST}/maas-api/v1/api-keys" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"sim-test-key","subscription":"sim-models-subscription"}' | jq -r '.key')

echo "API Key: ${API_KEY:0:20}..."
```

## Step 4: Test

Send a test request (OpenAI format, routed through the full IPP plugin chain):
```bash
curl -s "http://${GW_HOST}/llm-katan/sim-openai/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"sim-openai","messages":[{"role":"user","content":"Hello!"}]}'
```

Expected response (echo mode mirrors back the input):
```json
{
  "id": "chatcmpl-...",
  "model": "test",
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "[echo] model=test ... messages=1\nUser: Hello!"
    },
    "finish_reason": "stop"
  }]
}
```

Test all providers:
```bash
for model in sim-openai sim-anthropic sim-azure-openai sim-bedrock sim-vertex-openai; do
  echo "--- ${model} ---"
  curl -s "http://${GW_HOST}/llm-katan/${model}/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}" | python3 -m json.tool
done
```

## Dashboard

The simulator serves a live dashboard at:
```
https://<route-host>/dashboard
```

Get the URL:
```bash
echo "https://$(oc get route -n llm-katan -o jsonpath='{.items[0].spec.host}')/dashboard"
```

## Available Models

| Model Name | Provider Type | Gateway Path |
|------------|--------------|-------------|
| `sim-openai` | openai | `/llm-katan/sim-openai/v1/chat/completions` |
| `sim-anthropic` | anthropic | `/llm-katan/sim-anthropic/v1/chat/completions` |
| `sim-azure-openai` | azure-openai | `/llm-katan/sim-azure-openai/v1/chat/completions` |
| `sim-bedrock` | bedrock-openai | `/llm-katan/sim-bedrock/v1/chat/completions` |
| `sim-vertex-openai` | vertex-openai | `/llm-katan/sim-vertex-openai/v1/chat/completions` |

All requests use OpenAI chat completions format. IPP translates to each provider's native format before forwarding to the simulator.

## Customisation

Override defaults via `--set` or a values file:

```bash
# Different gateway
helm template sim ./deploy/llm-katan-simulator \
  --set gateway.name=my-gateway \
  --set gateway.namespace=my-ns \
  --skip-crds | kubectl apply --server-side --force-conflicts -f -

# Different model namespace
helm template sim ./deploy/llm-katan-simulator \
  --set models.namespace=my-models \
  --skip-crds | kubectl apply --server-side --force-conflicts -f -

# Enable key validation (rejects requests with wrong API keys at the simulator)
helm template sim ./deploy/llm-katan-simulator \
  --set simulator.validateKeys=true \
  --skip-crds | kubectl apply --server-side --force-conflicts -f -
```

See `values.yaml` for all options.

## Cleanup

Delete the ExternalModel CRs first — this lets the controller clean up the HTTPRoutes, Services, and ServiceEntries it created:

```bash
oc delete externalmodel -n llm-katan -l app.kubernetes.io/instance=sim

# Then remove the remaining chart resources
helm template sim ./deploy/llm-katan-simulator --skip-crds \
  | kubectl delete --ignore-not-found -f -

# Remove the namespace
oc delete namespace llm-katan

# Remove IPP (optional)
helm uninstall payload-processing -n openshift-ingress
```
