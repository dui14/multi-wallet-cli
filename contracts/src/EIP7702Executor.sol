// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

/// @title EIP7702Executor
/// @notice Batch executor for an EIP-7702 delegated account.
///         A failed call bubbles its revert data the way OpenZeppelin
///         Contracts v5.4.0 Address.verifyCallResult does. Only the
///         account itself may call execute.
contract EIP7702Executor {
    error Unauthorized();
    error FailedCall();
    receive() external payable {}

    struct Call {
        address to;
        uint256 value;
        bytes data;
    }

    function execute(Call[] calldata calls) external payable {
        if (msg.sender != address(this)) {
            revert Unauthorized();
        }
        for (uint256 i = 0; i < calls.length; ++i) {
            (bool success, bytes memory returndata) = calls[i].to.call{value: calls[i].value}(calls[i].data);
            if (!success) {
                if (returndata.length > 0) {
                    assembly {
                        revert(add(returndata, 0x20), mload(returndata))
                    }
                }
                revert FailedCall();
            }
        }
    }
}
