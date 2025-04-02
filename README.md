# WACElib

The general objective of this project is to build machine
learning-assisted web application firewall mechanisms for the
identification, analysis and prevention of computer attacks on web
applications. The main idea is to combine the flexibility provided by
the classification procedures obtained from machine learning models
with the codified knowledge integrated in the specification of the
[OWASP Core Rule Set](https://coreruleset.org/) used by the [ModSecurity WAF](https://www.modsecurity.org/) to detect attacks, while
reducing false positives. The next figure shows a high-level
overview of the architecture:

![WACE architecture overview](https://github.com/tilsor/ModSecIntl_wace_core/blob/main/docs/images/architecture.jpg?raw=true "WACE architecture overview")

This repository contains a library that provides the main functionalities of WACE.
Currently, WACE can be integrated as a library using this repository. For example, with Coraza WAF (ref). 
Also, it can be deployed as a server and consume its API via gRPC, see (ref). For example, it can be integrated with ModSecurity (ref).

## Usage

WACElib exports five functions, which one of them initializes WACElib and the remaining four allow the analysis of a transaction taking as input results from a WAF and from machine learning models.

The invocation of these operations must follow an order. The first of them is:

- Init - 
Initializes the internal structures of WACElib. This operation must be invoked only once, and is required for transaction analysis.

As for the operations for transaction analysis, it must be followed:

1. InitTransaction -
Allows the initiation of a transaction in WACE, a transaction identifier must be provided. This operation must be invoked only once.

2. Analyze - 
Indicates to WACE the analysis of a transaction, the models and their type must be indicated, as well as the content of the transaction to be analyzed.

3. CheckTransaction -
Returns the result of the analysis of a transaction, the decision algorithm must be indicated and the results of the WAF must be provided. This operation can be invoked multiple times, waiting for the result of the synchronous models that have been invoked so far in the Analyze function.

4. CloseTransaction - 
Ends the transaction associated with the provided identifier. This operation should be invoked only once when the transaction analysis is completed.

Remark: In the scenario that you want to invoke the CheckTransaction function multiple times, naturally the order will be affected, alternating with the Analyze function.

## Configuration

In order to use WACElib, the SetConfig(ConfigFileData) operation of the configstore package must be invoked. ConfigFileData is defined in this package (ref).

## Example

```golang

```