/**
 * Sell service client — creates and lists ServiceOffer custom resources
 * via the Kubernetes CustomObjectsApi.
 */

import { CustomObjectsApi, KubeConfig } from '@kubernetes/client-node';
import { ObjectCustomObjectsApi } from '@kubernetes/client-node/dist/gen/types/ObjectParamAPI';
import type {
  CreateServiceOfferRequest,
  ServiceOffer,
  ServiceOfferCondition,
  ServiceOfferType,
} from '@/types/sell';

const CRD_GROUP = 'obol.org';
const CRD_VERSION = 'v1alpha1';
const CRD_PLURAL = 'serviceoffers';

export class SellClient {
  private customApi: ObjectCustomObjectsApi;

  constructor() {
    const kc = new KubeConfig();
    if (process.env.KUBERNETES_SERVICE_HOST) {
      kc.loadFromCluster();
    } else {
      if (!process.env.NEXT_PUBLIC_KUBECONFIG) {
        throw new Error(
          'NEXT_PUBLIC_KUBECONFIG required for local development'
        );
      }
      kc.loadFromFile(process.env.NEXT_PUBLIC_KUBECONFIG);
    }
    this.customApi = kc.makeApiClient(CustomObjectsApi);
  }

  /**
   * Create a ServiceOffer CR in the target namespace.
   */
  async createServiceOffer(req: CreateServiceOfferRequest): Promise<void> {
    const body = {
      apiVersion: `${CRD_GROUP}/${CRD_VERSION}`,
      kind: 'ServiceOffer',
      metadata: {
        name: req.name,
        namespace: req.namespace,
      },
      spec: {
        type: 'http',
        upstream: {
          service: req.upstream,
          namespace: req.namespace,
          port: req.port,
          healthPath: req.healthPath || '/health',
        },
        payment: {
          scheme: 'exact',
          network: req.chain,
          payTo: req.wallet,
          maxTimeoutSeconds: 300,
          price: {
            perRequest: req.pricePerRequest,
          },
        },
      },
    };

    await this.customApi.createNamespacedCustomObject({
      group: CRD_GROUP,
      version: CRD_VERSION,
      namespace: req.namespace,
      plural: CRD_PLURAL,
      body,
    });
  }

  /**
   * List ServiceOffer CRs across all namespaces (or a specific one).
   */
  async listServiceOffers(namespace?: string): Promise<ServiceOffer[]> {
    const response = namespace
      ? await this.customApi.listNamespacedCustomObject({
          group: CRD_GROUP,
          version: CRD_VERSION,
          namespace,
          plural: CRD_PLURAL,
        })
      : await this.customApi.listClusterCustomObject({
          group: CRD_GROUP,
          version: CRD_VERSION,
          plural: CRD_PLURAL,
        });

    const items = (response as { items?: unknown[] }).items || [];
    return items.map(normalizeServiceOffer);
  }
}

function normalizeServiceOffer(raw: unknown): ServiceOffer {
  const item = raw as Record<string, unknown>;
  const metadata = (item.metadata as Record<string, unknown>) || {};
  const spec = (item.spec as Record<string, unknown>) || {};
  const status = (item.status as Record<string, unknown>) || {};

  const type = normalizeType(spec.type);
  const conditions = normalizeConditions(status.conditions);

  return {
    name: String(metadata.name ?? ''),
    namespace: String(metadata.namespace ?? ''),
    type,
    status: deriveReadyStatus(conditions),
    endpoint: typeof status.endpoint === 'string' ? status.endpoint : undefined,
    conditions,
    model: normalizeModel(spec.model),
    upstream: normalizeUpstream(spec.upstream),
    payment: normalizePayment(spec.payment),
    registration: normalizeRegistration(spec.registration),
    // agentLink and usage are filled in by the API route after joining
    // against the live agent list and Prometheus.
    agentLink: null,
    usage: { chargedRequests: null },
  };
}

function normalizeType(value: unknown): ServiceOfferType {
  if (value === 'inference' || value === 'fine-tuning' || value === 'http') {
    return value;
  }
  return 'http';
}

function normalizeConditions(value: unknown): ServiceOfferCondition[] {
  if (!Array.isArray(value)) return [];
  return value
    .filter(
      (c): c is Record<string, unknown> => typeof c === 'object' && c !== null
    )
    .map(c => ({
      type: String(c.type ?? ''),
      status: String(c.status ?? ''),
      message: String(c.message ?? ''),
    }));
}

function deriveReadyStatus(conditions: ServiceOfferCondition[]): string {
  const ready = conditions.find(c => c.type === 'Ready');
  return ready?.status === 'True' ? 'Ready' : 'Pending';
}

function normalizeModel(value: unknown) {
  if (typeof value !== 'object' || value === null) return undefined;
  const m = value as Record<string, unknown>;
  return {
    name: typeof m.name === 'string' ? m.name : undefined,
    runtime: typeof m.runtime === 'string' ? m.runtime : undefined,
  };
}

function normalizeUpstream(value: unknown) {
  if (typeof value !== 'object' || value === null) return undefined;
  const u = value as Record<string, unknown>;
  return {
    service: typeof u.service === 'string' ? u.service : undefined,
    namespace: typeof u.namespace === 'string' ? u.namespace : undefined,
    port: typeof u.port === 'number' ? u.port : undefined,
    healthPath: typeof u.healthPath === 'string' ? u.healthPath : undefined,
  };
}

function normalizePayment(value: unknown) {
  if (typeof value !== 'object' || value === null) return undefined;
  const p = value as Record<string, unknown>;
  const price = (p.price as Record<string, unknown>) || {};
  const asset = (p.asset as Record<string, unknown>) || {};
  return {
    scheme: typeof p.scheme === 'string' ? p.scheme : undefined,
    network: typeof p.network === 'string' ? p.network : undefined,
    payTo: typeof p.payTo === 'string' ? p.payTo : undefined,
    asset:
      typeof p.asset === 'object' && p.asset !== null
        ? {
            symbol: typeof asset.symbol === 'string' ? asset.symbol : undefined,
            decimals:
              typeof asset.decimals === 'number' ? asset.decimals : undefined,
            address:
              typeof asset.address === 'string' ? asset.address : undefined,
          }
        : undefined,
    price: {
      perRequest:
        typeof price.perRequest === 'string' ? price.perRequest : undefined,
      perMTok: typeof price.perMTok === 'string' ? price.perMTok : undefined,
      perHour: typeof price.perHour === 'string' ? price.perHour : undefined,
      perEpoch: typeof price.perEpoch === 'string' ? price.perEpoch : undefined,
    },
  };
}

function normalizeRegistration(value: unknown) {
  if (typeof value !== 'object' || value === null) return undefined;
  const r = value as Record<string, unknown>;
  return {
    enabled: typeof r.enabled === 'boolean' ? r.enabled : undefined,
    name: typeof r.name === 'string' ? r.name : undefined,
    description: typeof r.description === 'string' ? r.description : undefined,
    image: typeof r.image === 'string' ? r.image : undefined,
    skills: Array.isArray(r.skills)
      ? r.skills.filter((s): s is string => typeof s === 'string')
      : undefined,
    domains: Array.isArray(r.domains)
      ? r.domains.filter((d): d is string => typeof d === 'string')
      : undefined,
  };
}
