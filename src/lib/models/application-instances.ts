import { Type, Static } from '@sinclair/typebox';
import { Value } from '@sinclair/typebox/value';
import { CHAINS } from '@/types/dashboard';

//
// Base schema
//
export const BaseApplicationInstanceSchema = Type.Object({
  app: Type.Union([
    Type.Literal('helios'),
    Type.Literal('aztec'),
    Type.Literal('ethereum'),
    Type.Literal('erpc'),
  ]),
  id: Type.String(),
  creationTimestamp: Type.Date(),
});

//
// Shared endpoint schema
//
const RpcEndpointSchema = Type.Object({
  external: Type.String(),
  internal: Type.String(),
});

//
// App-specific schemas
//
export const HeliosInstanceDataSchema = Type.Object({
  network: Type.Union([
    Type.Literal(CHAINS.ETHEREUM),
    Type.Literal(CHAINS.SEPOLIA),
    Type.Literal(CHAINS.HOODI),
  ]),
  endpoints: Type.Object({
    rpc: RpcEndpointSchema,
  }),
});

export const AztecInstanceDataSchema = Type.Object({
  network: Type.Union([
    Type.Literal(CHAINS.ETHEREUM),
    Type.Literal(CHAINS.SEPOLIA),
    Type.Literal(CHAINS.HOODI),
  ]),
  endpoints: Type.Object({
    rpc: RpcEndpointSchema,
  }),
});

export const ErpcInstanceDataSchema = Type.Object({
  network: Type.Union([
    Type.Literal(CHAINS.ETHEREUM),
    Type.Literal(CHAINS.SEPOLIA),
    Type.Literal(CHAINS.HOODI),
  ]),
  endpoints: Type.Object({
    rpc: RpcEndpointSchema,
  }),
});

export const EthereumInstanceDataSchema = Type.Object({
  network: Type.Union([
    Type.Literal(CHAINS.ETHEREUM),
    Type.Literal(CHAINS.SEPOLIA),
    Type.Literal(CHAINS.HOODI),
  ]),
  execution: Type.Object({
    client: Type.String(),
    endpoints: Type.Object({
      rpc: RpcEndpointSchema,
    }),
  }),
  consensus: Type.Object({
    client: Type.String(),
    endpoints: Type.Object({
      rpc: RpcEndpointSchema,
    }),
  }),
});

//
// Extend base schema for full typed instances
//
export const HeliosInstanceSchema = Type.Intersect([
  BaseApplicationInstanceSchema,
  HeliosInstanceDataSchema,
]);

export const AztecInstanceSchema = Type.Intersect([
  BaseApplicationInstanceSchema,
  AztecInstanceDataSchema,
]);

export const ErpcInstanceSchema = Type.Intersect([
  BaseApplicationInstanceSchema,
  ErpcInstanceDataSchema,
]);

export const EthereumInstanceSchema = Type.Intersect([
  BaseApplicationInstanceSchema,
  EthereumInstanceDataSchema,
]);

//
// Types inferred automatically
//
export type BaseApplicationInstance = Static<
  typeof BaseApplicationInstanceSchema
>;
export type HeliosInstance = Static<typeof HeliosInstanceSchema> & {
  app: 'helios';
};
export type AztecInstance = Static<typeof AztecInstanceSchema> & {
  app: 'aztec';
};
export type EthereumInstance = Static<typeof EthereumInstanceSchema> & {
  app: 'ethereum';
};
export type ErpcInstance = Static<typeof ErpcInstanceSchema> & { app: 'erpc' };

//
// App → schema mapping
//
const AppInstanceDataSchemas = {
  helios: HeliosInstanceDataSchema,
  aztec: AztecInstanceDataSchema,
  ethereum: EthereumInstanceDataSchema,
  erpc: ErpcInstanceDataSchema,
} as const;

export type AnyApplicationInstance =
  | HeliosInstance
  | AztecInstance
  | EthereumInstance
  | ErpcInstance;

export type AppName = keyof typeof AppInstanceDataSchemas;

//
// Validation
//
export function validateApplicationInstance(
  app: string,
  data: unknown,
  base: BaseApplicationInstance
) {
  const schema = AppInstanceDataSchemas[app as AppName];
  if (!schema) {
    throw new Error(`No schema defined for app: ${app}`);
  }

  // Runtime validation using TypeBox
  const valid = Value.Check(schema, data);
  if (!valid) {
    const errors = [...Value.Errors(schema, data)]
      .map(e => `${e.path}: ${e.message}`)
      .join(', ');

    throw new Error(`Invalid data for app ${app}: ${errors}`);
  }

  const parsed = Value.Cast(schema, data);

  return { ...base, ...parsed };
}

export function isValidApp(app: string): app is AppName {
  return app in AppInstanceDataSchemas;
}

export function isEthereumInstance(
  instance: AnyApplicationInstance
): instance is EthereumInstance {
  return instance.app === 'ethereum';
}

export function isErpcInstance(
  instance: AnyApplicationInstance
): instance is ErpcInstance {
  return instance.app === 'erpc';
}
