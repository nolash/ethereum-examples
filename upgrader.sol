pragma solidity ^0.4.0; 

import './Store.sol';
import './AbstractMain.sol';

contract Upgrader {
	address public current;
	event Upgraded(address newaddress);
	function Upgrader() payable {
	}

	function create() {
		Store store = new Store();
		store.poke(); // store foo set to true
		AbstractMain main =  new AbstractMain();
		main.setStore(store);
		main.transfer(this.balance);
		current = main;
	}
	function upgrade(address _main) {
		AbstractMain newmain = AbstractMain(_main);
		require(newmain.owned()); // will throw if AbstractMain.transfer with our address hasn't been called
		AbstractMain oldmain = AbstractMain(current);
		Store store = oldmain.store();
		newmain.setStore(store); // will throw if store is already set
		oldmain.kill(newmain); 
		current = newmain; // we know we own the new contract, and moneys have been passed
		Upgraded(newmain);
	}
	function check() constant returns(bool, uint256) {
	    AbstractMain main = AbstractMain(current);
	    return (main.store().foo(), main.balance);
	}
}
