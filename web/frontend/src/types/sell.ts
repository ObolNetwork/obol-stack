export type ServiceOfferType = 'inference' | 'fine-tuning' | 'http' | 'agent';

export interface ServiceOfferModel {
  name?: string;
  runtime?: string;
}

export interface ServiceOfferAsset {
  symbol?: string;
  decimals?: number;
  address?: string;
  transferMethod?: string;
  eip712Name?: string;
  eip712Version?: string;
  eip712Domain?: {
    name?: string;
    version?: string;
  };
}

export interface ServiceOfferPrice {
  perRequest?: string;
  perMTok?: string;
  perHour?: string;
  perEpoch?: string;
}

export interface ServiceOfferPayment {
  scheme?: string;
  network?: string;
  payTo?: string;
  asset?: ServiceOfferAsset;
  price: ServiceOfferPrice;
}

export interface ServiceOfferRegistration {
  enabled?: boolean;
  name?: string;
  description?: string;
  image?: string;
  services?: ServiceOfferRegistrationService[];
  supportedTrust?: string[];
  skills?: string[];
  domains?: string[];
  metadata?: Record<string, string>;
}

export interface ServiceOfferRegistrationService {
  name?: string;
  endpoint?: string;
  version?: string;
}

export interface ServiceOfferAgentRef {
  name?: string;
  namespace?: string;
}

export interface ServiceOfferAgent {
  ref?: ServiceOfferAgentRef;
}

export interface ServiceOfferUpstream {
  service?: string;
  namespace?: string;
  port?: number;
  healthPath?: string;
}

export interface ServiceOfferAgentLink {
  runtime: 'hermes' | 'openclaw';
  id: string;
  namespace: string;
}

// Mirror of monetizeapi.ServiceOfferAgentResolution. The controller fills
// this in for type=agent offers from the linked Agent's status; spec.model
// is empty by design on agent offers, so this is the source of truth for
// display.
export interface ServiceOfferAgentResolution {
  runtime?: string;
  endpoint?: string;
  model?: string;
  skills?: string[];
}

export interface ServiceOfferUsage {
  chargedRequests: number | null;
  /** Per-chain charged-request counts over 7d window, keyed by `payment.network`. */
  chargedByChain?: Array<{ chain: string; requests: number }>;
  /** Charged requests over the last 24h (from PromQL increase()). */
  requests24h?: number | null;
  /** Unix seconds of the last successful settlement, if observed. */
  lastSettlementUnix?: number | null;
}

export interface ServiceOffer {
  name: string;
  namespace: string;
  type: ServiceOfferType;
  status?: string;
  /** Decimal price string from /api/services.json, before display formatting. */
  priceRaw?: string;
  /** Catalog price slot, e.g. perRequest, perMTok, perHour, perEpoch. */
  priceUnit?: string;
  /** Atomic-units string from /api/services.json for x402 signing preflight. */
  priceAtomicUnits?: string;
  /** CAIP-2 network identifier from /api/services.json, e.g. eip155:84532. */
  caip2Network?: string;
  /** Numeric EVM chain id from /api/services.json. */
  chainId?: number;
  /** True for controller-marked demo services in /api/services.json. */
  isDemo?: boolean;
  /** True when the payment route is live but ERC-8004 registration is pending. */
  registrationPending?: boolean;
  /** RFC3339 drain deadline from /api/services.json. Present only while draining. */
  drainEndsAt?: string;
  /** Optional seller-provided provenance metadata from spec.provenance. */
  provenance?: Record<string, string>;
  /** Legacy pause annotation, retained only so old CRs can still be labelled. */
  paused?: boolean;
  /** ISO-8601 of CR creation (from metadata.creationTimestamp). */
  createdAt?: string;
  endpoint?: string;
  conditions?: ServiceOfferCondition[];
  agent?: ServiceOfferAgent;
  model?: ServiceOfferModel;
  upstream?: ServiceOfferUpstream;
  payment?: ServiceOfferPayment;
  registration?: ServiceOfferRegistration;
  agentLink?: ServiceOfferAgentLink | null;
  agentResolution?: ServiceOfferAgentResolution | null;
  usage?: ServiceOfferUsage;
}

export interface ServiceOfferCondition {
  type: string;
  status: string;
  message: string;
}

export interface ServiceOfferAgentRefInput {
  name: string;
  namespace: string;
}

export interface CreateServiceOfferRequest {
  name: string;
  namespace: string;
  type?: ServiceOfferType;
  wallet: string;
  chain: string;
  token?: string;
  asset?: ServiceOfferAsset;
  pricePerRequest: string;
  /** Omitted for type=agent — controller resolves upstream from agent.ref. */
  upstream?: string;
  port?: number;
  healthPath?: string;
  /** Required for type=inference — maps to spec.model.name. */
  model?: string;
  modelRuntime?: string;
  /** Required for type=agent — maps to spec.agent.ref. */
  agentRef?: ServiceOfferAgentRefInput;
  /** Agent sell — storefront description (Agent CR objective when available). */
  description?: string;
  skills?: string[];
  registrationEnabled?: boolean;
}
