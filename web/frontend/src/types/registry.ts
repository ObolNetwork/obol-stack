/** Agent data from the 8004scan public API. */
export interface Agent8004 {
  token_id: string;
  chain_id: number;
  name: string;
  description: string;
  image_url: string;
  owner_address: string;
  x402_supported: boolean;
  supported_protocols: string[];
  services: Record<string, { endpoint?: string; tools?: string[] }> | null;
  total_score: number;
  star_count: number;
  is_verified: boolean;
  health_status: string | null;
  tags: string[];
  categories: string[];
  supported_trust_models: string[];
  created_tx_hash: string;
  is_testnet: boolean;
}

export interface Agent8004ListResponse {
  success: boolean;
  data: Agent8004[];
  meta: {
    pagination: {
      page: number;
      limit: number;
      total: number;
      hasMore: boolean;
    };
  };
}
