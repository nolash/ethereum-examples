pragma solitidy ^0.4.0;

import './Store.sol';

contract AbstractMain is owned {
	Store public store;
	function setStore(address _store) {
		store = Store(_store);
	}
	function kill(address _beneficiary) {
		require(isOwned());
		selfdestruct(_beneficiary);
	}
	function transfer(address _owner) onlyowner {
		owner = _owner;
	}
	function isOwned() constant returns(bool) {
		return owner == msg.sender;
	}
	function () payable {};
}
