import type {
  CreateServiceOfferRequest,
  ServiceOfferAsset,
} from '../../types/sell.ts';

const CRD_GROUP = 'obol.org';
const CRD_VERSION = 'v1alpha1';

export function buildServiceOfferBody(req: CreateServiceOfferRequest) {
  const payment: Record<string, unknown> = {
    scheme: 'exact',
    network: req.chain,
    payTo: req.wallet,
    maxTimeoutSeconds: 300,
    price: {
      perRequest: req.pricePerRequest,
    },
  };
  const asset = serviceOfferAssetBody(req.asset);
  if (asset) payment.asset = asset;

  const offerType = req.type ?? 'http';

  const spec: Record<string, unknown> = {
    type: offerType,
    payment,
    path: `/services/${req.name}`,
  };

  if (offerType === 'agent' && req.agentRef) {
    spec.agent = {
      ref: {
        name: req.agentRef.name,
        namespace: req.agentRef.namespace,
      },
    };
  } else {
    spec.upstream = {
      service: req.upstream ?? 'litellm',
      namespace: req.namespace,
      port: req.port ?? 4000,
      healthPath: req.healthPath || '/health',
    };
  }

  if (offerType === 'inference' && req.model) {
    spec.model = {
      name: req.model,
      runtime: req.modelRuntime ?? 'ollama',
    };
  }

  if (offerType === 'agent') {
    spec.registration = {
      enabled: req.registrationEnabled !== false,
      name: req.name,
      description: req.description?.trim() ?? '',
      skills: req.skills ?? [],
    };
  }

  return {
    apiVersion: `${CRD_GROUP}/${CRD_VERSION}`,
    kind: 'ServiceOffer',
    metadata: {
      name: req.name,
      namespace: req.namespace,
    },
    spec,
  };
}

function serviceOfferAssetBody(asset?: ServiceOfferAsset) {
  if (!asset) return undefined;
  return {
    address: asset.address,
    symbol: asset.symbol,
    decimals: asset.decimals,
    transferMethod: asset.transferMethod,
    eip712Name: asset.eip712Name ?? asset.eip712Domain?.name,
    eip712Version: asset.eip712Version ?? asset.eip712Domain?.version,
  };
}
