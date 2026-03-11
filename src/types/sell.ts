export interface ServiceOffer {
  name: string;
  namespace: string;
  status?: string;
  endpoint?: string;
  conditions?: ServiceOfferCondition[];
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
