export interface Service {
  name: string;
  namespace: string;
  type: string;
  model?: string;
  endpoint: string;
  price: string;
  priceRaw?: string;
  payTo: string;
  network: string;
  description: string;
  isDemo: boolean;
}
