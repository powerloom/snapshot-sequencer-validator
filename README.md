# Powerloom Decentralized Snapshot Sequencer and Validator

Refer to the [wiki](https://github.com/powerloom/snapshot-sequencer-validator/wiki) for more information.

## EIP-712 Signature Verification and Slot Validation

### Signature Verification
The snapshot sequencer now implements EIP-712 signature verification for submissions. This ensures the authenticity and integrity of submissions by verifying the cryptographic signature of the snapshotter.

#### Key Features
- Cryptographic signature verification using EIP-712 standard
- Signature-based snapshotter address recovery
- Optional slot validation against protocol state cache

### Slot Validation

#### Environment Variable: ENABLE_SLOT_VALIDATION
- **Purpose**: Enable optional validation of submission slots against protocol state
- **Default**: false
- **Requirement**: Requires protocol-state-cacher to be deployed

#### Configuration
To enable slot validation, set the following environment variable:
```bash
ENABLE_SLOT_VALIDATION=true
```

When enabled, the system will:
- Verify the submission slot against the protocol state cache
- Provide detailed error logging for signature verification failures
- Reject submissions that fail signature or slot validation

### Deployment Considerations
- Ensure proper configuration of ENABLE_SLOT_VALIDATION
- Have protocol-state-cacher deployed when slot validation is required
- Monitor logs for signature verification details
