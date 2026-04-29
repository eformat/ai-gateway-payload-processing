# Deploying the llm-katan Simulator on OpenShift

This guide deploys the [llm-katan](https://github.com/opendatahub-io/llm-katan) LLM provider simulator into an OpenShift cluster with the BBR (Body-Based Routing) payload-processing pipeline. The simulator runs in echo mode (no GPU, no model download) and provides mock endpoints for OpenAI, Anthropic, Azure OpenAI, Bedrock, and Vertex AI.

## Prerequisites

- `oc` CLI logged in to the target cluster
- `helm` v3 installed
- An Istio-based Gateway API Gateway already deployed (e.g. `maas-default-gateway`)
- The repo cloned locally:
  ```bash
  git clone https://github.com/opendatahub-io/ai-gateway-payload-processing.git
  cd ai-gateway-payload-processing
  ```

## Step 1: Install the ExternalModel CRD

The BBR `model-provider-resolver` plugin watches ExternalModel custom resources. This CRD is not bundled with the chart — install it once per cluster:

```bash
oc apply -f https://raw.githubusercontent.com/opendatahub-io/models-as-a-service/refs/heads/main/deployment/base/maas-controller/crd/bases/maas.opendatahub.io_externalmodels.yaml
```

Verify:
```bash
oc get crd externalmodels.maas.opendatahub.io
```

## Step 2: Deploy BBR Payload Processing

BBR must be installed **in the same namespace as the Gateway** (required for Istio EnvoyFilter `targetRefs`). Set the gateway name to match your cluster:

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

Disable Istio sidecar injection on the BBR pod (ext_proc uses self-signed TLS, which the sidecar intercepts and breaks):

```bash
oc patch deployment payload-processing -n ${GATEWAY_NAMESPACE} --type=merge \
  -p='{"spec":{"template":{"metadata":{"annotations":{"sidecar.istio.io/inject":"false"}}}}}'
```

Wait for rollout:
```bash
oc rollout status deployment/payload-processing -n ${GATEWAY_NAMESPACE} --timeout=120s
```

## Step 3: Deploy the llm-katan Simulator

```bash
helm install sim ./deploy/llm-katan-simulator
```

This creates a `llm-katan` namespace containing:

| Resource | Count | Purpose |
|----------|-------|---------|
| Deployment | 1 | llm-katan simulator pod (echo mode, TLS) |
| Service (ClusterIP) | 1 | Internal endpoint for the simulator |
| ServiceEntry + DestinationRule | 1 each | Istio routing with `insecureSkipVerify` for self-signed TLS |
| ExternalModel CRs | 5 | One per provider, watched by BBR |
| Secrets | 5 | Test API keys (labeled `bbr-managed`) |
| ExternalName Services | 5 | Per-provider services pointing to the simulator |
| HTTPRoutes | 5 | Per-provider routes attached to the gateway |
| Route | 1 | OpenShift route for the dashboard UI |

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

## Step 4: Test

Get the gateway hostname:
```bash
export GW_HOST=$(oc get gateway ${GATEWAY_NAME} -n ${GATEWAY_NAMESPACE} \
  -o jsonpath='{.spec.listeners[0].hostname}')
```

Send a test request (OpenAI format, routed through the full BBR plugin chain):
```bash
curl -s "http://${GW_HOST}/llm-katan/sim-openai/v1/chat/completions" \
  -H "Authorization: Bearer test" \
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
    -H "Authorization: Bearer test" \
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

All requests use OpenAI chat completions format. BBR translates to each provider's native format before forwarding to the simulator.

## Customisation

Override defaults via `--set` or a values file:

```bash
# Different gateway
helm install sim ./deploy/llm-katan-simulator \
  --set gateway.name=my-gateway \
  --set gateway.namespace=my-ns

# Different model namespace
helm install sim ./deploy/llm-katan-simulator \
  --set models.namespace=my-models

# Enable key validation (rejects requests with wrong API keys)
helm install sim ./deploy/llm-katan-simulator \
  --set simulator.validateKeys=true
```

See `values.yaml` for all options.

## Cleanup

```bash
# Remove simulator + all model resources
helm uninstall sim

# Remove the llm-katan namespace
oc delete namespace llm-katan

# Remove BBR
helm uninstall payload-processing -n openshift-ingress

# Remove ExternalModel CRD (optional — removes ALL ExternalModel resources cluster-wide)
oc delete crd externalmodels.maas.opendatahub.io
```
