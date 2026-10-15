/**
 * Sell service client — creates and lists ServiceOffer custom resources
 * via the Kubernetes CustomObjectsApi.
 */

import {
  CustomObjectsApi,
  PatchStrategy,
  setHeaderOptions,
} from '@kubernetes/client-node';
import { ObjectCustomObjectsApi } from '@kubernetes/client-node/dist/gen/types/ObjectParamAPI';
import type { CreateServiceOfferRequest, ServiceOffer } from '@/types/sell';
import { sharedKubeConfig } from '@/lib/services/kube';
import { buildServiceOfferBody } from './sell-create';
import { normalizeServiceOffer } from './sell-normalize';

const CRD_GROUP = 'obol.org';
const CRD_VERSION = 'v1alpha1';
const CRD_PLURAL = 'serviceoffers';

export class SellClient {
  private customApi: ObjectCustomObjectsApi;

  constructor() {
    const kc = sharedKubeConfig();
    this.customApi = kc.makeApiClient(CustomObjectsApi);
  }

  /**
   * Create a ServiceOffer CR in the target namespace.
   */
  async createServiceOffer(req: CreateServiceOfferRequest): Promise<void> {
    const body = buildServiceOfferBody(req);

    await this.customApi.createNamespacedCustomObject({
      group: CRD_GROUP,
      version: CRD_VERSION,
      namespace: req.namespace,
      plural: CRD_PLURAL,
      body,
    });
  }

  /**
   * Apply a merge patch to a ServiceOffer CR — used by /api/sell/stop to set
   * spec.drainAt + spec.drainGracePeriod. The controller observes these and
   * begins the drain countdown; the CR is not deleted here.
   *
   * The generated CustomObjectsApi.patchNamespacedCustomObject defaults to
   * `application/json-patch+json`, which would reject our merge-style object
   * body with 415, so pin the Content-Type via the client's header
   * middleware. Errors surface as ApiException (`.code` = HTTP status).
   */
  async patchServiceOffer(
    namespace: string,
    name: string,
    patch: Record<string, unknown>
  ): Promise<void> {
    await this.customApi.patchNamespacedCustomObject(
      {
        group: CRD_GROUP,
        version: CRD_VERSION,
        namespace,
        plural: CRD_PLURAL,
        name,
        body: patch,
        fieldManager: 'obol-stack-front-end',
      },
      setHeaderOptions('Content-Type', PatchStrategy.MergePatch)
    );
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
