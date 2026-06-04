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

  return {
    apiVersion: `${CRD_GROUP}/${CRD_VERSION}`,
    kind: 'ServiceOffer',
    metadata: {
      name: req.name,
      namespace: req.namespace,
    },
    spec: {
      type: req.type ?? 'http',
      upstream: {
        service: req.upstream,
        namespace: req.namespace,
        port: req.port,
        healthPath: req.healthPath || '/health',
      },
      payment,
    },
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
