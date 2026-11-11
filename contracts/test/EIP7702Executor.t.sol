// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

import {Test} from "forge-std/Test.sol";
import {EIP7702Executor} from "../src/EIP7702Executor.sol";

contract EIP7702ExecutorTest is Test {
    EIP7702Executor internal executor;
    address internal stranger = makeAddr("stranger");
    address internal owner = makeAddr("owner");
    address internal recipient = makeAddr("recipient");

    function setUp() public {
        executor = new EIP7702Executor();
        vm.deal(owner, 1 ether);
    }

    function testStrangerExecuteReverts() public {
        EIP7702Executor.Call[] memory calls = new EIP7702Executor.Call[](1);
        calls[0] = EIP7702Executor.Call({to: recipient, value: 0, data: ""});
        vm.prank(stranger);
        vm.expectRevert(EIP7702Executor.Unauthorized.selector);
        executor.execute(calls);
    }

    function testDelegatedOwnerCanSendValue() public {
        bytes memory designator = abi.encodePacked(hex"ef0100", bytes20(address(executor)));
        vm.etch(owner, designator);
        EIP7702Executor.Call[] memory calls = new EIP7702Executor.Call[](1);
        calls[0] = EIP7702Executor.Call({to: recipient, value: 0.1 ether, data: ""});
        vm.prank(owner);
        EIP7702Executor(payable(owner)).execute(calls);
        assertEq(recipient.balance, 0.1 ether);
    }

    function testStrangerCannotCallDelegatedAccount() public {
        bytes memory designator = abi.encodePacked(hex"ef0100", bytes20(address(executor)));
        vm.etch(owner, designator);
        EIP7702Executor.Call[] memory calls = new EIP7702Executor.Call[](1);
        calls[0] = EIP7702Executor.Call({to: recipient, value: 0, data: ""});
        vm.prank(stranger);
        vm.expectRevert(EIP7702Executor.Unauthorized.selector);
        EIP7702Executor(payable(owner)).execute(calls);
    }
}
