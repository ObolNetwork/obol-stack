export type ServiceOfferType = 'inference' | 'fine-tuning' | 'http' | 'agent';

export interface ServiceOfferModel {
  name?: string;
  runtime?: string;
}

export interface ServiceOfferAsset {
  symbol?: string;
  decimals?: number;
  address?: string;
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
  skills?: string[];
  domains?: string[];
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
}

export interface ServiceOffer {
  name: string;
  namespace: string;
  type: ServiceOfferType;
  status?: string;
  endpoint?: string;
  conditions?: ServiceOfferCondition[];
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

export interface CreateServiceOfferRequest {
  name: string;
  namespace: string;
  wallet: string;
  chain: string;
  pricePerRequest: string;
  upstream: string;
  port: number;
  healthPath?: string;
}
