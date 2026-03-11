/**
 * Sell service client — creates and lists ServiceOffer custom resources
 * via the Kubernetes CustomObjectsApi.
 */

import { CustomObjectsApi, KubeConfig } from '@kubernetes/client-node';
import { ObjectCustomObjectsApi } from '@kubernetes/client-node/dist/gen/types/ObjectParamAPI';
import type { CreateServiceOfferRequest, ServiceOffer } from '@/types/sell';

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

    const items = (response as any).items || [];
    return items.map((item: any) => ({
      name: item.metadata?.name,
      namespace: item.metadata?.namespace,
      status:
        item.status?.conditions?.find((c: any) => c.type === 'Ready')
          ?.status === 'True'
          ? 'Ready'
          : 'Pending',
      endpoint: item.status?.endpoint,
      conditions: item.status?.conditions || [],
    }));
  }
}
