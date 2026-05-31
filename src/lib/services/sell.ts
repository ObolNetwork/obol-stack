/**
 * Sell service client — creates and lists ServiceOffer custom resources
 * via the Kubernetes CustomObjectsApi.
 */

import {
  CustomObjectsApi,
  KubeConfig,
  PatchStrategy,
  setHeaderOptions,
} from '@kubernetes/client-node';
import { ObjectCustomObjectsApi } from '@kubernetes/client-node/dist/gen/types/ObjectParamAPI';
import https from 'node:https';
import type { CreateServiceOfferRequest, ServiceOffer } from '@/types/sell';
import { sharedKubeConfig } from '@/lib/services/kube';
import { buildServiceOfferBody } from './sell-create';
import { normalizeServiceOffer } from './sell-normalize';

const CRD_GROUP = 'obol.org';
const CRD_VERSION = 'v1alpha1';
const CRD_PLURAL = 'serviceoffers';

export class SellClient {
  private customApi: ObjectCustomObjectsApi;
  private kc: KubeConfig;

  constructor() {
    const kc = sharedKubeConfig();
    this.kc = kc;
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
   * Set or clear the `obol.org/paused` annotation on a ServiceOffer.
   *
   * Kept as a harmless no-op against the backend bundle (which replaces
   * pause-via-annotation with drain-via-spec) so the legacy
   * /api/sell/[name] route continues to compile and run. New stop flows
   * should call `patchServiceOffer` with spec.drainAt instead.
   */
  async setPaused(
    name: string,
    namespace: string,
    paused: boolean
  ): Promise<void> {
    await this.customApi.patchNamespacedCustomObject(
      {
        group: CRD_GROUP,
        version: CRD_VERSION,
        namespace,
        plural: CRD_PLURAL,
        name,
        body: {
          metadata: {
            annotations: {
              'obol.org/paused': paused ? 'true' : null,
            },
          },
        },
      },
      setHeaderOptions('Content-Type', PatchStrategy.MergePatch)
    );
  }

  /**
   * Delete a ServiceOffer CR. The controller's finalizer handles cleanup of
   * downstream HTTPRoute, Middleware, ReferenceGrant, and RegistrationRequest.
   */
  async deleteServiceOffer(name: string, namespace: string): Promise<void> {
    await this.customApi.deleteNamespacedCustomObject({
      group: CRD_GROUP,
      version: CRD_VERSION,
      namespace,
      plural: CRD_PLURAL,
      name,
    });
  }

  /**
   * Apply a merge patch to a ServiceOffer CR — used by /api/sell/stop to set
   * spec.drainAt + spec.drainGracePeriod. The controller observes these and
   * begins the drain countdown; the CR is not deleted here.
   *
   * The generated CustomObjectsApi.patchNamespacedCustomObject picks
   * `application/json-patch+json` as the default Content-Type, which would
   * reject our merge-style object body with 415. We use the lower-level
   * fetch transport with the KubeConfig auth so we can pin the right
   * Content-Type without bringing rxjs into the bundle just to wrap a
   * one-shot middleware.
   */
  async patchServiceOffer(
    namespace: string,
    name: string,
    patch: Record<string, unknown>
  ): Promise<void> {
    const cluster = this.kc.getCurrentCluster();
    if (!cluster) {
      throw new Error('no current cluster in kubeconfig');
    }

    const fetchOpts: https.RequestOptions = {};
    const init = (await this.kc.applyToFetchOptions(fetchOpts)) as RequestInit;

    const url =
      `${cluster.server}/apis/${CRD_GROUP}/${CRD_VERSION}` +
      `/namespaces/${encodeURIComponent(namespace)}` +
      `/${CRD_PLURAL}/${encodeURIComponent(name)}` +
      `?fieldManager=obol-stack-front-end`;

    const headers = new Headers(init.headers);
    headers.set('Content-Type', 'application/merge-patch+json');
    headers.set('Accept', 'application/json');

    const response = await fetch(url, {
      ...init,
      method: 'PATCH',
      headers,
      body: JSON.stringify(patch),
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      const err = new Error(
        `patch ServiceOffer ${namespace}/${name} failed: ${response.status} ${text}`
      ) as Error & { statusCode?: number };
      err.statusCode = response.status;
      throw err;
    }
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
