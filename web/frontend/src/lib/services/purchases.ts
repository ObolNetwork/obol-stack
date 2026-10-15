/**
 * Buyer-side service client — lists PurchaseRequest custom resources via the
 * Kubernetes CustomObjectsApi. Mirrors SellClient.
 */

import { CustomObjectsApi } from '@kubernetes/client-node';
import { ObjectCustomObjectsApi } from '@kubernetes/client-node/dist/gen/types/ObjectParamAPI';
import type {
  PurchaseAutoRefill,
  PurchaseCondition,
  PurchasePayment,
  PurchaseRequest,
  PurchaseStatus,
} from '@/types/purchase';
import { sharedKubeConfig } from '@/lib/services/kube';

const CRD_GROUP = 'obol.org';
const CRD_VERSION = 'v1alpha1';
const CRD_PLURAL = 'purchaserequests';

export class PurchasesClient {
  private customApi: ObjectCustomObjectsApi;

  constructor() {
    const kc = sharedKubeConfig();
    this.customApi = kc.makeApiClient(CustomObjectsApi);
  }

  async listPurchaseRequests(namespace?: string): Promise<PurchaseRequest[]> {
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
    return items.map(normalizePurchaseRequest);
  }
}

function normalizePurchaseRequest(raw: unknown): PurchaseRequest {
  const item = raw as Record<string, unknown>;
  const metadata = (item.metadata as Record<string, unknown>) || {};
  const spec = (item.spec as Record<string, unknown>) || {};
  const status = (item.status as Record<string, unknown>) || {};

  return {
    name: String(metadata.name ?? ''),
    namespace: String(metadata.namespace ?? ''),
    createdAt: String(metadata.creationTimestamp ?? ''),
    spec: {
      endpoint: String(spec.endpoint ?? ''),
      model: typeof spec.model === 'string' ? spec.model : undefined,
      count: toNumber(spec.count, 0),
      autoRefill: normalizeAutoRefill(spec.autoRefill),
      payment: normalizePayment(spec.payment),
    },
    status: normalizeStatus(status),
  };
}

function normalizeAutoRefill(value: unknown): PurchaseAutoRefill {
  const v = (value as Record<string, unknown>) ?? {};
  return {
    enabled: Boolean(v.enabled),
    threshold: typeof v.threshold === 'number' ? v.threshold : undefined,
    count: typeof v.count === 'number' ? v.count : undefined,
    maxTotal: typeof v.maxTotal === 'number' ? v.maxTotal : undefined,
    maxSpendPerDay:
      typeof v.maxSpendPerDay === 'string' ? v.maxSpendPerDay : undefined,
  };
}

function normalizePayment(value: unknown): PurchasePayment {
  const v = (value as Record<string, unknown>) ?? {};
  return {
    network: String(v.network ?? ''),
    payTo: String(v.payTo ?? ''),
    price: String(v.price ?? '0'),
    asset: String(v.asset ?? ''),
    assetSymbol: typeof v.assetSymbol === 'string' ? v.assetSymbol : undefined,
    assetDecimals:
      typeof v.assetDecimals === 'number' ? v.assetDecimals : undefined,
  };
}

function normalizeStatus(value: unknown): PurchaseStatus {
  const v = (value as Record<string, unknown>) ?? {};
  return {
    observedGeneration:
      typeof v.observedGeneration === 'number'
        ? v.observedGeneration
        : undefined,
    conditions: normalizeConditions(v.conditions),
    publicModel: typeof v.publicModel === 'string' ? v.publicModel : undefined,
    remaining: toNumber(v.remaining, 0),
    spent: toNumber(v.spent, 0),
    totalSigned: toNumber(v.totalSigned, 0),
    totalSpent: typeof v.totalSpent === 'string' ? v.totalSpent : undefined,
    probedAt: typeof v.probedAt === 'string' ? v.probedAt : undefined,
    probedPrice: typeof v.probedPrice === 'string' ? v.probedPrice : undefined,
    walletBalance:
      typeof v.walletBalance === 'string' ? v.walletBalance : undefined,
    signerAddress:
      typeof v.signerAddress === 'string' ? v.signerAddress : undefined,
  };
}

function normalizeConditions(value: unknown): PurchaseCondition[] {
  if (!Array.isArray(value)) return [];
  return value
    .filter(
      (c): c is Record<string, unknown> => typeof c === 'object' && c !== null
    )
    .map(c => ({
      type: String(c.type ?? ''),
      status: String(c.status ?? ''),
      reason: typeof c.reason === 'string' ? c.reason : undefined,
      message: typeof c.message === 'string' ? c.message : undefined,
      lastTransitionTime:
        typeof c.lastTransitionTime === 'string'
          ? c.lastTransitionTime
          : undefined,
    }));
}

function toNumber(value: unknown, fallback: number): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  return fallback;
}
