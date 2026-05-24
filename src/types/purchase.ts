/**
 * Buyer-side x402 types. Mirrors the PurchaseRequest CRD
 * (internal/embed/infrastructure/base/templates/purchaserequest-crd.yaml in
 * obol-stack) so the /api/marketplace/purchases handler can hand the frontend
 * a typed object instead of raw kube JSON.
 */

export interface PurchaseSpec {
  endpoint: string;
  model?: string;
  count: number;
  autoRefill: PurchaseAutoRefill;
  payment: PurchasePayment;
}

export interface PurchaseAutoRefill {
  enabled: boolean;
  threshold?: number;
  count?: number;
  maxTotal?: number;
  maxSpendPerDay?: string;
}

export interface PurchasePayment {
  network: string;
  payTo: string;
  price: string;
  asset: string;
  assetSymbol?: string;
  assetDecimals?: number;
}

export interface PurchaseCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
}

export interface PurchaseStatus {
  observedGeneration?: number;
  conditions: PurchaseCondition[];
  publicModel?: string;
  remaining: number;
  spent: number;
  totalSigned: number;
  totalSpent?: string;
  probedAt?: string;
  probedPrice?: string;
  walletBalance?: string;
  signerAddress?: string;
}

export interface PurchaseRequest {
  name: string;
  namespace: string;
  createdAt: string;
  spec: PurchaseSpec;
  status: PurchaseStatus;
}

export interface PurchaseListResponse {
  purchases: PurchaseRequest[];
}
