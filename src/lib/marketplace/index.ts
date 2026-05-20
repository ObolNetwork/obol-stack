/**
 * Barrel for the marketplace foundation: the catalog fetcher, pure filter
 * pipeline, and the public types both API routes and (in PR-B) the List and
 * Cards views consume.
 */

export * from './types';
export {
  fetchCatalog,
  mapCatalog,
  type CatalogSnapshot,
  type RawCatalogEntry,
} from './catalog';
export {
  computeFacets,
  filterItems,
  paginate,
  sortItems,
  type Paginated,
} from './filter';
export { parseFilters, applyPipeline } from './pipeline';
